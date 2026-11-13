// Observability persistence (P2.1): raw metric samples, their
// 5-minute downsampled aggregates, and health alerts.
//
// Budget (RFC D-02): raw rows live 24 h and are folded — in one
// transaction per pass — into metric_samples_5m aggregates that live
// 14 days. Fold + prune run on the sampler cadence; every statement is
// plain SQL so the retention story is auditable with sqlite3 itself.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MetricSample is one raw 15-second observation.
type MetricSample struct {
	ID                   int64
	TakenAt              time.Time
	CPUPercent           float64
	Load1, Load5, Load15 float64
	MemUsedBytes         int64
	MemAvailableBytes    int64
	MemTotalBytes        int64
	SwapUsedBytes        int64
	SwapTotalBytes       int64
	DisksJSON            string // [{path,total_bytes,available_bytes,used_percent}]
}

// MetricBucket5m is one downsampled 5-minute aggregate row.
type MetricBucket5m struct {
	ID              int64
	BucketStart     time.Time
	CPUAvg, CPUMax  float64
	Load5Avg        float64
	MemAvgBytes     int64
	MemMaxBytes     int64
	MemAvailableMin int64
	DiskWorstMax    float64
	SwapMaxBytes    int64
	SwapTotalBytes  int64
	SamplesCount    int64
}

// DiskPoint is one per-mount figure inside DisksJSON.
type DiskPoint struct {
	Path           string  `json:"path"`
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

// InsertSample stores one raw sample. Failures are logged upstream and
// never fatal: observability must not take the daemon down.
func (s *Store) InsertSample(ctx context.Context, m MetricSample) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO metric_samples
        (taken_at, cpu_percent, load1, load5, load15,
         mem_used_bytes, mem_available_bytes, mem_total_bytes,
         swap_used_bytes, swap_total_bytes, disks)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TakenAt.UTC().Format(time.RFC3339Nano), m.CPUPercent, m.Load1, m.Load5, m.Load15,
		m.MemUsedBytes, m.MemAvailableBytes, m.MemTotalBytes,
		m.SwapUsedBytes, m.SwapTotalBytes, m.DisksJSON)
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	return nil
}

// FoldAndRetain folds complete 5-minute buckets older than retainRaw
// into metric_samples_5m (averages + maxima), deletes the folded raw
// rows, and prunes aggregates older than keep5m. It returns the number
// of raw rows folded and aggregate rows pruned. Idempotent: an already
// folded bucket produces no aggregate row (checked per bucket) and the
// raw rows for it are gone.
func (s *Store) FoldAndRetain(ctx context.Context, now time.Time, retainRaw, keep5m time.Duration) (folded, pruned int64, err error) {
	rawCutoff := now.Add(-retainRaw)
	// Only COMPLETE buckets are folded: bucket end must be <= cutoff.
	firstIncomplete := now.Truncate(5 * time.Minute).Add(-5 * time.Minute)

	rows, err := s.db.QueryContext(ctx, `
SELECT id, taken_at, cpu_percent, load5,
       mem_used_bytes, mem_available_bytes,
       swap_used_bytes, swap_total_bytes, disks
FROM metric_samples
WHERE taken_at < ?`, rawCutoff.Format(time.RFC3339Nano))
	if err != nil {
		return 0, 0, fmt.Errorf("load fold candidates: %w", err)
	}
	type rawRow struct {
		id        int64
		taken     time.Time
		cpu       float64
		load5     float64
		memUsed   int64
		memAvail  int64
		swapUsed  int64
		swapTotal int64
		worstDisk float64
	}
	var raws []rawRow
	for rows.Next() {
		var r rawRow
		var taken string
		var disksJSON string
		if err := rows.Scan(&r.id, &taken, &r.cpu, &r.load5, &r.memUsed, &r.memAvail, &r.swapUsed, &r.swapTotal, &disksJSON); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan fold candidate: %w", err)
		}
		r.taken = parseTime(taken)
		var disks []DiskPoint
		_ = json.Unmarshal([]byte(disksJSON), &disks)
		for _, d := range disks {
			if d.UsedPercent > r.worstDisk {
				r.worstDisk = d.UsedPercent
			}
		}
		raws = append(raws, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate fold candidates: %w", err)
	}

	// Group by 5-minute bucket, skipping the newest (incomplete) one.
	buckets := map[string]*struct {
		start    time.Time
		rows     []rawRow
		cpuMax   float64
		memMax   int64
		availMin int64
		swapMax  int64
	}{}
	var foldIDs []int64
	for _, r := range raws {
		b := r.taken.Truncate(5 * time.Minute)
		if !b.Add(5 * time.Minute).Before(firstIncomplete.Add(5 * time.Minute)) {
			// bucket end >= first incomplete bucket end → skip
			if !b.Before(firstIncomplete) {
				continue
			}
		}
		key := b.Format(time.RFC3339Nano)
		g := buckets[key]
		if g == nil {
			g = &struct {
				start    time.Time
				rows     []rawRow
				cpuMax   float64
				memMax   int64
				availMin int64
				swapMax  int64
			}{start: b, availMin: r.memAvail, memMax: r.memUsed, swapMax: r.swapUsed}
			buckets[key] = g
		}
		if r.cpu > g.cpuMax {
			g.cpuMax = r.cpu
		}
		if r.memUsed > g.memMax {
			g.memMax = r.memUsed
		}
		if r.memAvail < g.availMin {
			g.availMin = r.memAvail
		}
		if r.swapUsed > g.swapMax {
			g.swapMax = r.swapUsed
		}
		g.rows = append(g.rows, r)
		foldIDs = append(foldIDs, r.id)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin fold: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := buckets[k]
		var sumCPU, sumLoad, sumMem, sumWorst float64
		var sumSwap int64
		for _, r := range g.rows {
			sumCPU += r.cpu
			sumLoad += r.load5
			sumMem += float64(r.memUsed)
			sumWorst += r.worstDisk
			sumSwap += r.swapUsed
		}
		n := float64(len(g.rows))
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM metric_samples_5m WHERE bucket_start = ?`, k).Scan(&exists); err != nil {
			return 0, 0, fmt.Errorf("bucket check: %w", err)
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO metric_samples_5m
        (bucket_start, cpu_avg, cpu_max, load5_avg,
         mem_avg_bytes, mem_max_bytes, mem_available_min,
         disk_worst_max, swap_max_bytes, swap_total_bytes, samples_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				k, sumCPU/n, g.cpuMax, sumLoad/n,
				int64(sumMem/n), g.memMax, g.availMin,
				sumWorst/n, g.swapMax, g.rows[len(g.rows)-1].swapTotal, len(g.rows)); err != nil {
				return 0, 0, fmt.Errorf("insert bucket: %w", err)
			}
		}
	}
	if len(foldIDs) > 0 {
		// foldIDs came from the sorted-by-id query; safe for IN (…).
		var sb []byte
		for i, id := range foldIDs {
			if i > 0 {
				sb = append(sb, ',')
			}
			sb = append(sb, []byte(fmt.Sprintf("%d", id))...)
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM metric_samples WHERE id IN (`+string(sb)+`)`)
		if err != nil {
			return 0, 0, fmt.Errorf("delete folded rows: %w", err)
		}
		folded, _ = res.RowsAffected()
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM metric_samples_5m WHERE bucket_start < ?`,
		now.Add(-keep5m).Format(time.RFC3339Nano))
	if err != nil {
		return 0, 0, fmt.Errorf("prune buckets: %w", err)
	}
	pruned, _ = res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit fold: %w", err)
	}
	return folded, pruned, nil
}

// HistoryPoint is one blended history point (raw or 5m resolution).
type HistoryPoint struct {
	T          time.Time `json:"t"`
	CPUAvg     float64   `json:"cpu_avg"`
	CPUMax     float64   `json:"cpu_max"`
	MemAvg     int64     `json:"mem_avg_bytes"`
	MemMax     int64     `json:"mem_max_bytes"`
	DiskWorst  float64   `json:"disk_worst"`
	Resolution string    `json:"resolution"` // "raw" | "5m"
}

// MetricHistory returns history points newer than from, raw rows for
// the raw window and aggregates beyond it, ascending. The API caps
// the window at 14 days (the aggregate retention).
func (s *Store) MetricHistory(ctx context.Context, from time.Time) ([]HistoryPoint, error) {
	fromStr := from.UTC().Format(time.RFC3339Nano)

	rawRows, err := s.db.QueryContext(ctx, `
SELECT taken_at, cpu_percent, mem_used_bytes, disks
FROM metric_samples WHERE taken_at > ? ORDER BY taken_at`, fromStr)
	if err != nil {
		return nil, fmt.Errorf("history raw: %w", err)
	}
	defer rawRows.Close()
	var out []HistoryPoint
	for rawRows.Next() {
		var taken, disksJSON string
		var cpu float64
		var mem int64
		if err := rawRows.Scan(&taken, &cpu, &mem, &disksJSON); err != nil {
			return nil, fmt.Errorf("scan history raw: %w", err)
		}
		var disks []DiskPoint
		_ = json.Unmarshal([]byte(disksJSON), &disks)
		worst := 0.0
		for _, d := range disks {
			if d.UsedPercent > worst {
				worst = d.UsedPercent
			}
		}
		out = append(out, HistoryPoint{
			T: parseTime(taken), CPUAvg: cpu, CPUMax: cpu,
			MemAvg: mem, MemMax: mem, DiskWorst: worst,
			Resolution: "raw",
		})
	}
	if err := rawRows.Err(); err != nil {
		return nil, err
	}

	buckets, err := s.db.QueryContext(ctx, `
SELECT bucket_start, cpu_avg, cpu_max, mem_avg_bytes, mem_max_bytes, disk_worst_max
FROM metric_samples_5m WHERE bucket_start > ? ORDER BY bucket_start`, fromStr)
	if err != nil {
		return nil, fmt.Errorf("history buckets: %w", err)
	}
	defer buckets.Close()
	for buckets.Next() {
		var bs string
		var cpuAvg, cpuMax, worst float64
		var memAvg, memMax int64
		if err := buckets.Scan(&bs, &cpuAvg, &cpuMax, &memAvg, &memMax, &worst); err != nil {
			return nil, fmt.Errorf("scan history bucket: %w", err)
		}
		out = append(out, HistoryPoint{
			T: parseTime(bs), CPUAvg: cpuAvg, CPUMax: cpuMax,
			MemAvg: memAvg, MemMax: memMax, DiskWorst: worst,
			Resolution: "5m",
		})
	}
	if err := buckets.Err(); err != nil {
		return nil, err
	}
	// Raw and 5m tiers never overlap in time; a sort keeps the blend
	// strictly ascending regardless of fold timing.
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// ---------------------------------------------------------------------------
// Health alerts
// ---------------------------------------------------------------------------

// HealthAlert is one deduplicated alert row.
type HealthAlert struct {
	ID          int64
	Kind        string
	Subject     string
	Severity    string // warn | crit
	Message     string
	Value       float64
	Threshold   float64
	State       string // open | resolved
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	ResolvedAt  time.Time
}

// UpsertAlert opens or refreshes the (kind, subject) alert row.
// Escalation warn→crit (or message change) is applied in place;
// re-opening a resolved row resets its first-seen window.
func (s *Store) UpsertAlert(ctx context.Context, kind, subject, severity, message string, value, threshold float64, now time.Time) error {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO health_alerts
        (kind, subject, severity, message, value, threshold,
         state, first_seen_at, last_seen_at, resolved_at)
VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?, '')
ON CONFLICT (kind, subject) DO UPDATE SET
        severity = excluded.severity,
        message  = excluded.message,
        value    = excluded.value,
        threshold= excluded.threshold,
        state    = 'open',
        last_seen_at = excluded.last_seen_at,
        resolved_at  = ''`,
		kind, subject, severity, message, value, threshold, nowStr, nowStr)
	if err != nil {
		return fmt.Errorf("upsert alert: %w", err)
	}
	return nil
}

// ResolveAlert closes an open alert row. Returns whether a row was
// actually open (the caller uses this to decide about the audit event).
func (s *Store) ResolveAlert(ctx context.Context, kind, subject string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE health_alerts SET state = 'resolved', resolved_at = ?, last_seen_at = ?
WHERE kind = ? AND subject = ? AND state = 'open'`,
		now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), kind, subject)
	if err != nil {
		return false, fmt.Errorf("resolve alert: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListAlerts returns open alerts plus alerts resolved within since.
func (s *Store) ListAlerts(ctx context.Context, since time.Time) ([]HealthAlert, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, subject, severity, message, value, threshold,
       state, first_seen_at, last_seen_at, resolved_at
FROM health_alerts
WHERE state = 'open' OR (state = 'resolved' AND resolved_at > ?)
ORDER BY CASE state WHEN 'open' THEN 0 ELSE 1 END,
         CASE severity WHEN 'crit' THEN 0 ELSE 1 END,
         last_seen_at DESC`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	var out []HealthAlert
	for rows.Next() {
		var a HealthAlert
		var first, last, resolved string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Subject, &a.Severity, &a.Message,
			&a.Value, &a.Threshold, &a.State, &first, &last, &resolved); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		a.FirstSeenAt = parseTime(first)
		a.LastSeenAt = parseTime(last)
		a.ResolvedAt = parseTime(resolved)
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenAlerts returns all currently open alerts (the evaluator seeds
// its streak state from this at daemon start-up, so a restart does not
// double-open or instantly flap-resolve existing conditions).
func (s *Store) OpenAlerts(ctx context.Context) ([]HealthAlert, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, subject, severity, message, value, threshold,
       state, first_seen_at, last_seen_at, resolved_at
FROM health_alerts WHERE state = 'open'`)
	if err != nil {
		return nil, fmt.Errorf("open alerts: %w", err)
	}
	defer rows.Close()
	var out []HealthAlert
	for rows.Next() {
		var a HealthAlert
		var first, last, resolved string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Subject, &a.Severity, &a.Message,
			&a.Value, &a.Threshold, &a.State, &first, &last, &resolved); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		a.FirstSeenAt = parseTime(first)
		a.LastSeenAt = parseTime(last)
		a.ResolvedAt = parseTime(resolved)
		out = append(out, a)
	}
	return out, rows.Err()
}

// MetricBucketsForTest returns every 5-minute aggregate row keyed by
// bucket_start (used by the metrics tests to pin the downsample math;
// the production read path is MetricHistory).
func (s *Store) MetricBucketsForTest(ctx context.Context) (map[string]*MetricBucket5m, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT bucket_start, cpu_avg, cpu_max, load5_avg, mem_avg_bytes, mem_max_bytes,
       mem_available_min, disk_worst_max, swap_max_bytes, swap_total_bytes, samples_count
FROM metric_samples_5m`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*MetricBucket5m{}
	for rows.Next() {
		var b MetricBucket5m
		var start string
		if err := rows.Scan(&start, &b.CPUAvg, &b.CPUMax, &b.Load5Avg, &b.MemAvgBytes,
			&b.MemMaxBytes, &b.MemAvailableMin, &b.DiskWorstMax, &b.SwapMaxBytes,
			&b.SwapTotalBytes, &b.SamplesCount); err != nil {
			return nil, err
		}
		b.BucketStart = parseTime(start)
		out[start] = &b
	}
	return out, rows.Err()
}
