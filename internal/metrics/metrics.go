// Package metrics collects server metrics strictly on demand from /proc
// and statfs(2). There is deliberately no background sampling loop: every
// read of GET /api/v1/server/metrics triggers exactly one sampling pass.
package metrics

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CPUSample is one aggregate sample of the /proc/stat "cpu" line.
type CPUSample struct {
	Total uint64 // all jiffies
	Idle  uint64 // idle + iowait jiffies
}

// ParseProcStat parses the aggregate "cpu" line of /proc/stat.
func ParseProcStat(r io.Reader) (CPUSample, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue // skip cpu0, cpu1, ... lines
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return CPUSample{}, fmt.Errorf("unexpected aggregate cpu line %q", line)
		}
		vals := make([]uint64, 0, len(fields)-1)
		for _, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return CPUSample{}, fmt.Errorf("parse cpu field %q: %w", f, err)
			}
			vals = append(vals, v)
		}
		var total uint64
		for _, v := range vals {
			total += v
		}
		idle := vals[3] // idle
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		return CPUSample{Total: total, Idle: idle}, nil
	}
	if err := sc.Err(); err != nil {
		return CPUSample{}, err
	}
	return CPUSample{}, fmt.Errorf("no aggregate cpu line found")
}

// UsagePercent returns the busy percentage between two CPU samples.
func UsagePercent(a, b CPUSample) float64 {
	dt := float64(b.Total) - float64(a.Total)
	di := float64(b.Idle) - float64(a.Idle)
	if dt <= 0 || di < 0 {
		return 0
	}
	return clampPercent((dt - di) / dt * 100)
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// Memory is a snapshot of RAM and swap usage from /proc/meminfo.
type Memory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	BuffersBytes   uint64  `json:"buffers_bytes"`
	CachedBytes    uint64  `json:"cached_bytes"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapUsedBytes  uint64  `json:"swap_used_bytes"`
}

// ParseMeminfo parses /proc/meminfo (values are reported in "kB", i.e.
// 1024-byte units) into a Memory snapshot. Used = Total - Available,
// which matches how the kernel computes reclaimable pressure.
func ParseMeminfo(r io.Reader) (Memory, error) {
	fields := map[string]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		rest := strings.Fields(line[idx+1:])
		if len(rest) == 0 {
			continue
		}
		v, err := strconv.ParseUint(rest[0], 10, 64)
		if err != nil {
			continue
		}
		fields[line[:idx]] = v * 1024
	}
	if err := sc.Err(); err != nil {
		return Memory{}, err
	}
	total := fields["MemTotal"]
	avail := fields["MemAvailable"]
	if avail == 0 {
		avail = fields["MemFree"]
	}
	if avail > total {
		avail = total
	}
	used := total - avail
	pct := 0.0
	if total > 0 {
		pct = clampPercent(float64(used) / float64(total) * 100)
	}
	swapTotal := fields["SwapTotal"]
	swapFree := fields["SwapFree"]
	if swapFree > swapTotal {
		swapFree = swapTotal
	}
	cached := fields["Cached"] + fields["SReclaimable"]
	return Memory{
		TotalBytes:     total,
		AvailableBytes: avail,
		UsedBytes:      used,
		UsedPercent:    pct,
		BuffersBytes:   fields["Buffers"],
		CachedBytes:    cached,
		SwapTotalBytes: swapTotal,
		SwapUsedBytes:  swapTotal - swapFree,
	}, nil
}

// ParseLoadavg parses /proc/loadavg ("0.10 0.15 0.20 1/233 4321").
func ParseLoadavg(r io.Reader) (l1, l5, l15 float64, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected loadavg content")
	}
	if l1, err = strconv.ParseFloat(parts[0], 64); err != nil {
		return 0, 0, 0, fmt.Errorf("parse load field %q: %w", parts[0], err)
	}
	if l5, err = strconv.ParseFloat(parts[1], 64); err != nil {
		return 0, 0, 0, fmt.Errorf("parse load field %q: %w", parts[1], err)
	}
	if l15, err = strconv.ParseFloat(parts[2], 64); err != nil {
		return 0, 0, 0, fmt.Errorf("parse load field %q: %w", parts[2], err)
	}
	return l1, l5, l15, nil
}

// ParseUptime parses /proc/uptime ("12345.67 0.00").
func ParseUptime(r io.Reader) (float64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 0, fmt.Errorf("unexpected uptime content")
	}
	return strconv.ParseFloat(parts[0], 64)
}

// Disk is a statfs(2) snapshot of one filesystem.
type Disk struct {
	Path           string  `json:"path"`
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

// DiskUsage stats path and returns usage figures.
func DiskUsage(path string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	bs := uint64(st.Bsize)
	total := st.Blocks * bs
	avail := st.Bavail * bs
	free := st.Bfree * bs
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = clampPercent(float64(used) / float64(total) * 100)
	}
	return Disk{
		Path:           path,
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: avail,
		UsedPercent:    pct,
	}, nil
}

// CPU is the CPU section of a snapshot.
type CPU struct {
	Cores        int     `json:"cores"`
	UsagePercent float64 `json:"usage_percent"`
	Load1        float64 `json:"load_1m"`
	Load5        float64 `json:"load_5m"`
	Load15       float64 `json:"load_15m"`
}

// Snapshot is the full response body of GET /api/v1/server/metrics.
type Snapshot struct {
	Timestamp     time.Time `json:"timestamp"`
	CPU           CPU       `json:"cpu"`
	Memory        Memory    `json:"memory"`
	Disk          []Disk    `json:"disk"`
	UptimeSeconds float64   `json:"uptime_seconds"`
}

// Collector produces on-demand metric snapshots. It keeps exactly one
// previous CPU sample in memory to compute the busy delta between
// consecutive requests; it never samples in the background.
type Collector struct {
	mu        sync.Mutex
	prev      *CPUSample
	diskPaths []string
}

// NewCollector builds a Collector that reports the given filesystem paths.
func NewCollector(diskPaths []string) *Collector {
	return &Collector{diskPaths: diskPaths}
}

// Snapshot takes exactly one on-demand sampling pass.
func (c *Collector) Snapshot() (*Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	usage, err := c.cpuUsagePercent()
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	l1, l5, l15, err := readLoadavg()
	if err != nil {
		return nil, fmt.Errorf("loadavg: %w", err)
	}
	mem, err := readMemory()
	if err != nil {
		return nil, fmt.Errorf("meminfo: %w", err)
	}
	disks := make([]Disk, 0, len(c.diskPaths))
	for _, p := range c.diskPaths {
		d, err := DiskUsage(p)
		if err != nil {
			return nil, fmt.Errorf("disk: %w", err)
		}
		disks = append(disks, d)
	}
	up, err := readUptime()
	if err != nil {
		return nil, fmt.Errorf("uptime: %w", err)
	}

	return &Snapshot{
		Timestamp: time.Now().UTC(),
		CPU: CPU{
			Cores:        runtime.NumCPU(),
			UsagePercent: usage,
			Load1:        l1,
			Load5:        l5,
			Load15:       l15,
		},
		Memory:        mem,
		Disk:          disks,
		UptimeSeconds: up,
	}, nil
}

// cpuUsagePercent computes busy% since the previous call. On the first
// call it takes two samples 250 ms apart so the very first response is
// already meaningful.
func (c *Collector) cpuUsagePercent() (float64, error) {
	cur, err := readCPUSample()
	if err != nil {
		return 0, err
	}
	if c.prev == nil {
		time.Sleep(250 * time.Millisecond)
		second, err := readCPUSample()
		if err != nil {
			return 0, err
		}
		c.prev = &second
		return UsagePercent(cur, second), nil
	}
	pct := UsagePercent(*c.prev, cur)
	c.prev = &cur
	return pct, nil
}

func readCPUSample() (CPUSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUSample{}, err
	}
	defer f.Close()
	return ParseProcStat(f)
}

func readLoadavg() (l1, l5, l15 float64, err error) {
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	return ParseLoadavg(f)
}

func readMemory() (Memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	defer f.Close()
	return ParseMeminfo(f)
}

func readUptime() (float64, error) {
	f, err := os.Open("/proc/uptime")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return ParseUptime(f)
}
