package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
	storetest "github.com/backupamunga-ops/drevanta_server_manager/internal/store/storetest"
)

func newSampler(t *testing.T, st *store.Store) (*Sampler, *[]map[string]any) {
	t.Helper()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewSampler(NewCollector([]string{"/"}), st)
	events := &[]map[string]any{}
	s.Audit = func(_ context.Context, actor, action, target string, detail map[string]any) {
		d := map[string]any{"actor": actor, "action": action, "target": target}
		for k, v := range detail {
			d[k] = v
		}
		*events = append(*events, d)
	}
	return s, events
}

func snapshotWith(diskPct float64, availPct float64, swapPct float64, load5 float64) *Snapshot {
	memTotal := uint64(1000)
	memAvail := uint64(float64(memTotal) * availPct / 100)
	memUsed := memTotal - memAvail
	swapTotal := uint64(100)
	return &Snapshot{
		Timestamp: time.Now().UTC(),
		CPU:       CPU{Cores: 2, UsagePercent: 10, Load1: load5, Load5: load5, Load15: load5},
		Memory: Memory{
			TotalBytes: memTotal, AvailableBytes: memAvail, UsedBytes: memUsed,
			SwapTotalBytes: swapTotal, SwapUsedBytes: uint64(float64(swapTotal) * swapPct / 100),
		},
		Disk:          []Disk{{Path: "/", TotalBytes: 1000, UsedBytes: uint64(diskPct * 10), AvailableBytes: uint64(1000 - diskPct*10), UsedPercent: diskPct}},
		UptimeSeconds: 100,
	}
}

func evalTimes(s *Sampler, snap *Snapshot, n int) {
	for i := 0; i < n; i++ {
		s.evaluate(context.Background(), snap, time.Now().UTC())
	}
}

func findAlert(t *testing.T, st *store.Store, kind, subject string) *store.HealthAlert {
	t.Helper()
	open, err := st.OpenAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range open {
		if a.Kind == kind && a.Subject == subject {
			return &a
		}
	}
	return nil
}

func TestAlertOpensAfterTwoHitsAndResolvesAfterTwoClears(t *testing.T) {
	st := storetest.Open(t)
	s, events := newSampler(t, st)

	// One hit only: anti-flap keeps it closed.
	s.evaluate(context.Background(), snapshotWith(95, 50, 0, 0.1), time.Now().UTC())
	if a := findAlert(t, st, "disk", "/"); a != nil {
		t.Fatalf("alert opened after a single tick: %+v", a)
	}

	// Second consecutive hit opens it at crit severity.
	evalTimes(s, snapshotWith(95, 50, 0, 0.1), 1)
	a := findAlert(t, st, "disk", "/")
	if a == nil || a.Severity != "crit" || a.State != "open" {
		t.Fatalf("expected open crit disk alert, got %+v", a)
	}
	assertAuditContains(t, *events, "alert_opened")

	// One clear tick: still open (anti-flap).
	s.evaluate(context.Background(), snapshotWith(50, 50, 0, 0.1), time.Now().UTC())
	if a := findAlert(t, st, "disk", "/"); a == nil {
		t.Fatal("alert resolved after a single clear tick (anti-flap violated)")
	}

	// Second clear tick resolves it.
	s.evaluate(context.Background(), snapshotWith(50, 50, 0, 0.1), time.Now().UTC())
	if a := findAlert(t, st, "disk", "/"); a != nil {
		t.Fatalf("alert still open after two clear ticks: %+v", a)
	}
	assertAuditContains(t, *events, "alert_resolved")
}

func TestAlertEscalatesInPlace(t *testing.T) {
	st := storetest.Open(t)
	s, _ := newSampler(t, st)

	evalTimes(s, snapshotWith(82, 50, 0, 0.1), 2)
	a := findAlert(t, st, "disk", "/")
	if a == nil || a.Severity != "warn" {
		t.Fatalf("expected warn alert, got %+v", a)
	}
	evalTimes(s, snapshotWith(92, 50, 0, 0.1), 1)
	open, err := st.OpenAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("escalation must not duplicate rows: %d open", len(open))
	}
	if open[0].Severity != "crit" {
		t.Fatalf("severity = %s, want crit (escalated in place)", open[0].Severity)
	}
}

func TestLoadAlertNeedsThreeConsecutiveTicks(t *testing.T) {
	st := storetest.Open(t)
	s, _ := newSampler(t, st)

	evalTimes(s, snapshotWith(20, 50, 0, 5.0), 2) // load5=5 > 2×2 cores
	if a := findAlert(t, st, "load", "load"); a != nil {
		t.Fatalf("load alert opened after 2 ticks, want 3: %+v", a)
	}
	evalTimes(s, snapshotWith(20, 50, 0, 5.0), 1)
	if a := findAlert(t, st, "load", "load"); a == nil {
		t.Fatal("load alert not open after 3 consecutive ticks")
	}
}

func TestMemoryAndSwapAlerts(t *testing.T) {
	st := storetest.Open(t)
	s, _ := newSampler(t, st)

	// 8% available → warn; 3% → crit.
	evalTimes(s, snapshotWith(20, 8, 0, 0.1), 2)
	a := findAlert(t, st, "memory", "memory")
	if a == nil || a.Severity != "warn" {
		t.Fatalf("memory warn alert: %+v", a)
	}
	evalTimes(s, snapshotWith(20, 3, 0, 0.1), 1)
	open, _ := st.OpenAlerts(context.Background())
	for _, x := range open {
		if x.Kind == "memory" && x.Severity != "crit" {
			t.Fatalf("memory escalation missing: %+v", x)
		}
	}

	// Swap 85% → warn.
	evalTimes(s, snapshotWith(20, 50, 85, 0.1), 2)
	if a := findAlert(t, st, "swap", "swap"); a == nil {
		t.Fatal("swap alert missing")
	}
}

func assertAuditContains(t *testing.T, events []map[string]any, action string) {
	t.Helper()
	for _, e := range events {
		if e["action"] == action {
			return
		}
	}
	t.Fatalf("audit events missing %q: %v", action, events)
}

func TestFoldAndRetainAndIdempotence(t *testing.T) {
	st := storetest.Open(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(5 * time.Minute)

	// 20 raw samples in an old complete bucket (avg 40, max 80),
	// 4 samples in a second old bucket, 4 recent samples inside the
	// raw window (must survive).
	old := now.Add(-2 * time.Hour)
	for i := 0; i < 20; i++ {
		mustInsert(t, st, old.Add(time.Duration(i)*15*time.Second), 40+float64(i)*2)
	}
	mustInsert(t, st, now.Add(-45*time.Minute), 10)
	mustInsert(t, st, now.Add(-45*time.Minute+15*time.Second), 20)
	mustInsert(t, st, now.Add(-45*time.Minute+30*time.Second), 30)
	mustInsert(t, st, now.Add(-45*time.Minute+45*time.Second), 40)
	for i := 0; i < 4; i++ {
		mustInsert(t, st, now.Add(-time.Duration(i+1)*15*time.Second), 5)
	}

	folded, pruned, err := st.FoldAndRetain(ctx, now.Add(time.Minute), 30*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if folded != 24 {
		t.Fatalf("folded = %d, want 24", folded)
	}

	// Bucket aggregates correct: first bucket avg=58, max=78, n=20.
	buckets, err := st.MetricBucketsForTest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2 (recent raw must NOT fold): %+v", len(buckets), buckets)
	}
	b0 := buckets[old.Truncate(5*time.Minute).Format(time.RFC3339Nano)]
	if b0 == nil {
		t.Fatalf("first old bucket missing: %+v", buckets)
	}
	if b0.SamplesCount != 20 || b0.CPUAvg != 59 || b0.CPUMax != 78 {
		t.Fatalf("bucket math wrong: %+v", b0)
	}

	// Idempotence: a second pass folds nothing new and adds no rows.
	folded2, _, err := st.FoldAndRetain(ctx, now.Add(time.Minute), 30*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if folded2 != 0 {
		t.Fatalf("second fold moved %d rows, want 0", folded2)
	}
	buckets2, _ := st.MetricBucketsForTest(ctx)
	if len(buckets2) != 2 {
		t.Fatalf("duplicate buckets after idempotent pass: %d", len(buckets2))
	}

	// Prune: aggregates older than keep5m disappear.
	folded3, pruned, err := st.FoldAndRetain(ctx, now.Add(time.Minute), 30*time.Minute, 30*time.Minute)
	_ = folded3
	_ = pruned
	if err != nil {
		t.Fatal(err)
	}
	buckets3, _ := st.MetricBucketsForTest(ctx)
	if len(buckets3) != 0 {
		t.Fatalf("prune left %d buckets, want 0", len(buckets3))
	}
}

func mustInsert(t *testing.T, st *store.Store, at time.Time, cpu float64) {
	t.Helper()
	err := st.InsertSample(context.Background(), store.MetricSample{
		TakenAt: at, CPUPercent: cpu,
		Load1: 0.1, Load5: 0.1, Load15: 0.1,
		MemUsedBytes: 100, MemAvailableBytes: 900, MemTotalBytes: 1000,
		DisksJSON: `[{"path":"/","total_bytes":1000,"available_bytes":500,"used_percent":50}]`,
	})
	if err != nil {
		t.Fatalf("insert sample at %s: %v", at, err)
	}
}
