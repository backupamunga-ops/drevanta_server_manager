// Process list (RFC D-05): computed from /proc on request, never
// stored. CPU% is measured over a 500 ms in-request delta window, the
// same technique the metrics collector uses for aggregate CPU.
package metrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// procClockTicks is the kernel USER_HZ on Linux (fixed at 100 for the
// architectures this platform targets; jiffy math below relies on it).
const procClockTicks = 100.0

// procCPUDeltaWindow is how long the in-request measurement window is.
const procCPUDeltaWindow = 500 * time.Millisecond

// ProcInfo is one row of the process list.
type ProcInfo struct {
	PID        int     `json:"pid"`
	PPID       int     `json:"ppid"`
	User       string  `json:"user"`
	State      string  `json:"state"`
	RSSBytes   uint64  `json:"rss_bytes"`
	CPUPercent float64 `json:"cpu_percent"`
	Cmdline    string  `json:"cmdline"`
}

// procCPUSnapshot is one pass over per-process CPU jiffies.
type procCPUSnapshot map[int]uint64

// readProcCPUSnapshot walks /proc and returns utime+stime jiffies per
// numeric pid.
func readProcCPUSnapshot() (procCPUSnapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	snap := procCPUSnapshot{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a process directory
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // raced with process exit
		}
		utime, stime, ok := parseStatCPUFields(string(data))
		if !ok {
			continue
		}
		snap[pid] = utime + stime
	}
	return snap, nil
}

// parseStatCPUFields extracts utime/stime (fields 14/15) from a
// /proc/<pid>/stat line. The comm field may contain spaces and is
// parenthesized, so parsing starts after the LAST ')'.
func parseStatCPUFields(stat string) (utime, stime uint64, ok bool) {
	idx := strings.LastIndex(stat, ")")
	if idx < 0 || idx+2 >= len(stat) {
		return 0, 0, false
	}
	fields := strings.Fields(stat[idx+2:])
	// After comm, field 1 is state; utime is field 12 (14 overall),
	// stime field 13 (15 overall).
	if len(fields) < 15 {
		return 0, 0, false
	}
	u, err1 := strconv.ParseUint(fields[12], 10, 64)
	s, err2 := strconv.ParseUint(fields[13], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return u, s, true
}

// procStaticInfo is the non-CPU metadata of one process.
type procStaticInfo struct {
	ppid    int
	state   string
	rss     uint64 // bytes
	cmdline string
	uid     int
}

// readProcStatic reads state/ppid/statm-rss/uid/cmdline for one pid.
func readProcStatic(pid string) (procStaticInfo, error) {
	var info procStaticInfo
	statData, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return info, err
	}
	stat := string(statData)
	idx := strings.LastIndex(stat, ")")
	if idx < 0 || idx+2 >= len(stat) {
		return info, fmt.Errorf("malformed stat for pid %s", pid)
	}
	fields := strings.Fields(stat[idx+2:])
	if len(fields) < 3 {
		return info, fmt.Errorf("short stat for pid %s", pid)
	}
	info.state = fields[0]
	ppid, _ := strconv.Atoi(fields[1])
	info.ppid = ppid
	// RSS is field 24 overall (23 after comm), in pages.
	if len(fields) >= 23 {
		pages, _ := strconv.ParseUint(fields[22], 10, 64)
		info.rss = pages * uint64(os.Getpagesize())
	}
	uid := -1
	if statusData, err := os.ReadFile("/proc/" + pid + "/status"); err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					uid, _ = strconv.Atoi(parts[1])
				}
				break
			}
		}
	}
	info.uid = uid
	if cmdData, err := os.ReadFile("/proc/" + pid + "/cmdline"); err == nil {
		cmd := strings.ReplaceAll(string(cmdData), "\x00", " ")
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 120 {
			cmd = cmd[:117] + "..."
		}
		info.cmdline = cmd
	}
	if info.cmdline == "" {
		// Kernel threads show empty cmdline: fall back to the comm name.
		if i := strings.Index(stat, "("); i >= 0 && idx > i {
			info.cmdline = "[" + stat[i+1:idx] + "]"
		}
	}
	return info, nil
}

// username resolves a uid to a name; unknown uids stay numeric.
func username(uid int) string {
	if uid < 0 {
		return "?"
	}
	if u, err := lookupUser(uid); err == nil && u.Username != "" {
		return u.Username
	}
	return strconv.Itoa(uid)
}

// MeasuredOverMs reports the CPU measurement window in milliseconds
// (the API surfaces it so the UI can be honest about the half-second
// sampling gap).
func MeasuredOverMs() int { return int(procCPUDeltaWindow.Milliseconds()) }

// Processes returns up to top processes sorted by CPU% (desc). The
// CPU% window is procCPUDeltaWindow; kernel threads (empty cmdline)
// are reported with their [comm] name.
func Processes(top int) ([]ProcInfo, error) {
	first, err := readProcCPUSnapshot()
	if err != nil {
		return nil, err
	}
	time.Sleep(procCPUDeltaWindow)
	second, err := readProcCPUSnapshot()
	if err != nil {
		return nil, err
	}

	out := make([]ProcInfo, 0, len(second))
	for pid, jiffies2 := range second {
		jiffies1 := first[pid]
		delta := float64(jiffies2 - jiffies1)
		// CPU% of ONE core: jiffies / (window × HZ) × 100. A process
		// pinning one core reads 100%, two cores 200% — the convention
		// top(1) uses.
		cpuPct := delta / (procCPUDeltaWindow.Seconds() * procClockTicks) * 100
		if cpuPct < 0 {
			cpuPct = 0 // pid reuse within the window
		}
		pidStr := strconv.Itoa(pid)
		info, err := readProcStatic(pidStr)
		if err != nil {
			continue // raced with process exit
		}
		out = append(out, ProcInfo{
			PID:        pid,
			PPID:       info.ppid,
			User:       username(info.uid),
			State:      info.state,
			RSSBytes:   info.rss,
			CPUPercent: cpuPct,
			Cmdline:    info.cmdline,
		})
	}
	sortByCPU(out)
	if len(out) > top {
		out = out[:top]
	}
	return out, nil
}
