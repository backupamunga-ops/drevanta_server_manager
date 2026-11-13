// The P2.1 in-process sampler (RFC D-01, D-02, D-04): one goroutine in
// drevantad that collects a Snapshot every tick, stores the raw row,
// evaluates the alert conditions with anti-flap streaks, and on the
// maintenance cadence folds old raw rows into 5-minute aggregates and
// prunes both tiers. It must never take the daemon down: every error
// is logged and the loop continues.
package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// Alert thresholds (RFC D-04). Constants in this milestone; tuning
// knobs are explicitly deferred to P2.5.
const (
	alertDiskWarn  = 80.0 // % used on any mount
	alertDiskCrit  = 90.0
	alertMemWarn   = 10.0 // % available (below)
	alertMemCrit   = 5.0
	alertSwapWarn  = 80.0 // % of swap used
	alertSwapCrit  = 90.0
	alertLoadWarn  = 2.0 // × cores (load5), held 3 ticks
	alertLoadTicks = 3

	defaultInterval  = 15 * time.Second
	defaultRetainRaw = 24 * time.Hour
	defaultKeep5m    = 14 * 24 * time.Hour
	defaultFoldEvery = 15 * time.Minute
)

// openStreak is how many consecutive ticks a condition must hold to
// open (or escalate) an alert; clearStreak the same to resolve.
const openStreak = 2

// Sampler runs the metric collection loop and the alert evaluator.
type Sampler struct {
	Collector *Collector
	Store     *store.Store
	Audit     func(ctx context.Context, actor, action, target string, detail map[string]any)

	Interval  time.Duration // sample cadence (default 15 s)
	RetainRaw time.Duration // raw retention (default 24 h)
	Keep5m    time.Duration // aggregate retention (default 14 d)
	FoldEvery time.Duration // maintenance cadence (default 15 min)

	// Now allows clock injection in tests.
	Now func() time.Time

	// condition streaks, keyed by kind:subject (anti-flap state)
	hits   map[string]int
	misses map[string]int
}

// NewSampler builds a sampler with the RFC defaults applied.
func NewSampler(c *Collector, st *store.Store) *Sampler {
	return &Sampler{
		Collector: c,
		Store:     st,
		Interval:  defaultInterval,
		RetainRaw: defaultRetainRaw,
		Keep5m:    defaultKeep5m,
		FoldEvery: defaultFoldEvery,
		Now:       time.Now,
		hits:      map[string]int{},
		misses:    map[string]int{},
	}
}

// Run drives the loop until ctx is cancelled. It collects immediately
// once so a fresh instance has data right away.
func (s *Sampler) Run(ctx context.Context) {
	// Seed anti-flap state from open rows so a daemon restart does not
	// re-open resolved alerts or instantly resolve open ones.
	if open, err := s.Store.OpenAlerts(ctx); err == nil {
		for _, a := range open {
			s.hits[a.Kind+":"+a.Subject] = openStreak
		}
	} else {
		slog.Warn("sampler: seed open alerts", "err", err)
	}

	if err := s.tick(ctx); err != nil {
		slog.Warn("sampler: initial tick", "err", err)
	}
	sampleTicker := time.NewTicker(s.Interval)
	defer sampleTicker.Stop()
	foldTicker := time.NewTicker(s.FoldEvery)
	defer foldTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sampleTicker.C:
			if err := s.tick(ctx); err != nil {
				slog.Warn("sampler: tick failed", "err", err)
			}
		case <-foldTicker.C:
			folded, pruned, err := s.Store.FoldAndRetain(ctx, s.Now(), s.RetainRaw, s.Keep5m)
			if err != nil {
				slog.Warn("sampler: fold/retain failed", "err", err)
				continue
			}
			if folded > 0 || pruned > 0 {
				slog.Info("sampler: retention pass", "folded", folded, "pruned_buckets", pruned)
			}
		}
	}
}

// tick collects one snapshot, stores it and evaluates alerts.
func (s *Sampler) tick(ctx context.Context) error {
	now := s.Now().UTC()
	snap, err := s.Collector.Snapshot()
	if err != nil {
		return err
	}
	disks := make([]store.DiskPoint, 0, len(snap.Disk))
	for _, d := range snap.Disk {
		disks = append(disks, store.DiskPoint{
			Path:           d.Path,
			TotalBytes:     d.TotalBytes,
			AvailableBytes: d.AvailableBytes,
			UsedPercent:    d.UsedPercent,
		})
	}
	disksJSON, err := json.Marshal(disks)
	if err != nil {
		disksJSON = []byte("[]")
	}
	sample := store.MetricSample{
		TakenAt:           now,
		CPUPercent:        snap.CPU.UsagePercent,
		Load1:             snap.CPU.Load1,
		Load5:             snap.CPU.Load5,
		Load15:            snap.CPU.Load15,
		MemUsedBytes:      int64(snap.Memory.UsedBytes),
		MemAvailableBytes: int64(snap.Memory.AvailableBytes),
		MemTotalBytes:     int64(snap.Memory.TotalBytes),
		SwapUsedBytes:     int64(snap.Memory.SwapUsedBytes),
		SwapTotalBytes:    int64(snap.Memory.SwapTotalBytes),
		DisksJSON:         string(disksJSON),
	}
	if err := s.Store.InsertSample(ctx, sample); err != nil {
		return err
	}
	s.evaluate(ctx, snap, now)
	return nil
}

// condition describes one evaluated alert condition. requiredHits is
// the anti-flap open threshold in consecutive ticks (2 by default; the
// load condition needs 3 per D-04).
type condition struct {
	kind         string
	subject      string
	hit          bool
	severity     string // warn | crit
	message      string
	value        float64
	threshold    float64
	requiredHits int
}

// evaluate computes all conditions and applies the anti-flap state
// machine: 2 consecutive hits open (or escalate), 2 consecutive misses
// resolve. Transitions are audited.
func (s *Sampler) evaluate(ctx context.Context, snap *Snapshot, now time.Time) {
	conds := s.conditions(snap)
	seen := map[string]bool{}
	for _, c := range conds {
		key := c.kind + ":" + c.subject
		seen[key] = true
		if c.hit {
			s.hits[key]++
			delete(s.misses, key)
			if s.hits[key] >= c.requiredHits {
				if err := s.Store.UpsertAlert(ctx, c.kind, c.subject, c.severity, c.message, c.value, c.threshold, now); err != nil {
					slog.Warn("sampler: upsert alert", "err", err)
					continue
				}
				if s.Audit != nil && s.hits[key] == c.requiredHits {
					s.Audit(ctx, "drevantad", "alert_opened", "health_alert",
						map[string]any{"kind": c.kind, "subject": c.subject,
							"severity": c.severity, "value": c.value, "threshold": c.threshold})
				}
			}
		} else {
			s.misses[key]++
			delete(s.hits, key)
			if s.misses[key] >= openStreak {
				resolved, err := s.Store.ResolveAlert(ctx, c.kind, c.subject, now)
				if err != nil {
					slog.Warn("sampler: resolve alert", "err", err)
					continue
				}
				if resolved && s.Audit != nil {
					s.Audit(ctx, "drevantad", "alert_resolved", "health_alert",
						map[string]any{"kind": c.kind, "subject": c.subject})
				}
				delete(s.misses, key)
			}
		}
	}
	// Conditions that vanished entirely (e.g., a mount disappeared):
	// treat as miss so the alert resolves instead of lingering forever.
	for key := range s.hits {
		if !seen[key] {
			s.misses[key]++
			delete(s.hits, key)
		}
	}
}

// conditions derives every alert condition from one snapshot (D-04).
func (s *Sampler) conditions(snap *Snapshot) []condition {
	var out []condition

	for _, d := range snap.Disk {
		thr := alertDiskWarn
		sev := "warn"
		if d.UsedPercent >= alertDiskCrit {
			thr = alertDiskCrit
			sev = "crit"
		}
		out = append(out, condition{
			kind: "disk", subject: d.Path,
			hit: d.UsedPercent >= alertDiskWarn, severity: sev,
			message: "filesystem usage", value: d.UsedPercent, threshold: thr,
			requiredHits: openStreak,
		})
	}

	memTotal := snap.Memory.TotalBytes
	if memTotal > 0 {
		availPct := float64(snap.Memory.AvailableBytes) / float64(memTotal) * 100
		sev := "warn"
		thr := alertMemWarn
		if availPct <= alertMemCrit {
			sev = "crit"
			thr = alertMemCrit
		}
		out = append(out, condition{
			kind: "memory", subject: "memory",
			hit: availPct <= alertMemWarn, severity: sev,
			message: "memory available", value: availPct, threshold: thr,
			requiredHits: openStreak,
		})
	}

	if snap.Memory.SwapTotalBytes > 0 {
		swapPct := float64(snap.Memory.SwapUsedBytes) / float64(snap.Memory.SwapTotalBytes) * 100
		sev := "warn"
		thr := alertSwapWarn
		if swapPct >= alertSwapCrit {
			sev = "crit"
			thr = alertSwapCrit
		}
		out = append(out, condition{
			kind: "swap", subject: "swap",
			hit: swapPct >= alertSwapWarn, severity: sev,
			message: "swap in use", value: swapPct, threshold: thr,
			requiredHits: openStreak,
		})
	}

	loadThreshold := float64(snap.CPU.Cores) * alertLoadWarn
	out = append(out, condition{
		kind: "load", subject: "load",
		hit: snap.CPU.Load5 > loadThreshold, severity: "warn",
		message: "load5 above cores × 2", value: snap.CPU.Load5, threshold: loadThreshold,
		requiredHits: alertLoadTicks,
	})
	return out
}
