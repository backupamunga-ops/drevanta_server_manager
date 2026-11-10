package metrics

import (
	"strings"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	fixture := "cpu  100 0 50 800 10 0 10 0 0 0\n" +
		"cpu0 50 0 25 400 5 0 5 0 0 0\n" +
		"cpu1 50 0 25 400 5 0 5 0 0 0\n" +
		"intr 12345\n"
	sample, err := ParseProcStat(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseProcStat: %v", err)
	}
	// total = 100+0+50+800+10+0+10 = 970; idle = 800 + iowait 10 = 810
	if sample.Total != 970 {
		t.Fatalf("Total = %d, want 970", sample.Total)
	}
	if sample.Idle != 810 {
		t.Fatalf("Idle = %d, want 810", sample.Idle)
	}
}

func TestUsagePercent(t *testing.T) {
	a := CPUSample{Total: 1000, Idle: 900}
	b := CPUSample{Total: 1200, Idle: 1000}
	// dt = 200, di = 100 -> 50% busy
	if got := UsagePercent(a, b); got < 49.9 || got > 50.1 {
		t.Fatalf("UsagePercent = %f, want 50", got)
	}
	// idle grows more than total -> clamp to 0
	c := CPUSample{Total: 1000, Idle: 950}
	if got := UsagePercent(a, c); got != 0 {
		t.Fatalf("UsagePercent negative case = %f, want 0", got)
	}
	// identical samples -> 0 (avoid division by zero)
	if got := UsagePercent(a, a); got != 0 {
		t.Fatalf("UsagePercent same sample = %f, want 0", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	fixture := `MemTotal:       16000000 kB
MemFree:         2000000 kB
MemAvailable:    8000000 kB
Buffers:          500000 kB
Cached:          3000000 kB
SwapTotal:       2000000 kB
SwapFree:        1000000 kB
SReclaimable:     300000 kB
Shmem:            100000 kB
HugePages_Total:       0
`
	m, err := ParseMeminfo(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseMeminfo: %v", err)
	}
	if m.TotalBytes != 16000000*1024 {
		t.Fatalf("TotalBytes = %d", m.TotalBytes)
	}
	if m.AvailableBytes != 8000000*1024 {
		t.Fatalf("AvailableBytes = %d", m.AvailableBytes)
	}
	if m.UsedBytes != 8000000*1024 {
		t.Fatalf("UsedBytes = %d", m.UsedBytes)
	}
	if m.UsedPercent < 49.9 || m.UsedPercent > 50.1 {
		t.Fatalf("UsedPercent = %f, want 50", m.UsedPercent)
	}
	if m.BuffersBytes != 500000*1024 {
		t.Fatalf("BuffersBytes = %d", m.BuffersBytes)
	}
	if m.CachedBytes != 3300000*1024 { // Cached + SReclaimable
		t.Fatalf("CachedBytes = %d", m.CachedBytes)
	}
	if m.SwapTotalBytes != 2000000*1024 {
		t.Fatalf("SwapTotalBytes = %d", m.SwapTotalBytes)
	}
	if m.SwapUsedBytes != 1000000*1024 {
		t.Fatalf("SwapUsedBytes = %d", m.SwapUsedBytes)
	}
}

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg(strings.NewReader("0.10 0.25 0.50 1/233 4321\n"))
	if err != nil {
		t.Fatalf("ParseLoadavg: %v", err)
	}
	if l1 != 0.10 || l5 != 0.25 || l15 != 0.50 {
		t.Fatalf("loads = %f %f %f", l1, l5, l15)
	}
}

func TestParseUptime(t *testing.T) {
	up, err := ParseUptime(strings.NewReader("12345.67 45678.90\n"))
	if err != nil {
		t.Fatalf("ParseUptime: %v", err)
	}
	if up != 12345.67 {
		t.Fatalf("uptime = %f", up)
	}
}
