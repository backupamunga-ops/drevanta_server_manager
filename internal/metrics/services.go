// Process-list sorting + user lookup, and the systemd service health
// reader (RFC D-06): exactly two `systemctl` calls, no dbus, no root,
// no mutations.
package metrics

import (
	"os/exec"
	"os/user"
	"sort"
	"strconv"
	"strings"
)

// lookupUser wraps os/user.LookupId (split out for test stubbing).
var lookupUser = func(uid int) (*user.User, error) {
	return user.LookupId(strconv.Itoa(uid))
}

// sortByCPU sorts most-CPU-first; ties break by pid for stable output.
func sortByCPU(p []ProcInfo) {
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].CPUPercent != p[j].CPUPercent {
			return p[i].CPUPercent > p[j].CPUPercent
		}
		return p[i].PID < p[j].PID
	})
}

// ServiceUnit is one systemd service and its health.
type ServiceUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
	Enabled     string `json:"enabled"` // enabled | disabled | static | …
}

// systemdServiceSummary is the rollup shown on the overview.
type SystemdSummary struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

// runSystemctl executes one systemctl query with a bounded output and
// no pager. Failures (no systemd, restricted container) surface as
// errors the API turns into 503 with an honest message.
func runSystemctl(args ...string) (string, error) {
	cmd := exec.Command("systemctl", append(args, "--no-pager", "--no-legend")...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Services lists all service units with load/active/sub state joined
// with unit-file enablement. Read-only by construction: it only runs
// the two list queries below, never a mutation verb.
func Services() ([]ServiceUnit, error) {
	unitsOut, err := runSystemctl("list-units", "--type=service", "--all")
	if err != nil {
		return nil, err
	}
	enabledByUnit := map[string]string{}
	if filesOut, ferr := runSystemctl("list-unit-files", "--type=service"); ferr == nil {
		for _, line := range strings.Split(filesOut, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				enabledByUnit[fields[0]] = fields[1]
			}
		}
	}

	var out []ServiceUnit
	for _, line := range strings.Split(unitsOut, "\n") {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// list-units columns: UNIT LOAD ACTIVE SUB DESCRIPTION…
		unit := fields[0]
		load, active, sub := fields[1], fields[2], fields[3]
		desc := ""
		if idx := strings.Index(line, sub); idx >= 0 {
			desc = strings.TrimSpace(line[idx+len(sub):])
		}
		u := ServiceUnit{
			Unit:        unit,
			Load:        load,
			Active:      active,
			Sub:         sub,
			Description: desc,
			Enabled:     enabledByUnit[unit],
		}
		out = append(out, u)
	}
	sortUnits(out)
	return out, nil
}

// sortUnits puts failed/maintenance first, then active, then the rest,
// alphabetical within each group (the operator looks for trouble).
func sortUnits(u []ServiceUnit) {
	rank := func(s ServiceUnit) int {
		switch {
		case s.Active == "failed":
			return 0
		case s.Active == "maintenance":
			return 1
		case s.Active == "active":
			return 2
		}
		return 3
	}
	sort.SliceStable(u, func(i, j int) bool {
		ri, rj := rank(u[i]), rank(u[j])
		if ri != rj {
			return ri < rj
		}
		return u[i].Unit < u[j].Unit
	})
}

// SystemdSummaryOf rolls a unit list into the overview counts.
func SystemdSummaryOf(units []ServiceUnit) SystemdSummary {
	var s SystemdSummary
	s.Total = len(units)
	for _, u := range units {
		switch u.Active {
		case "active":
			s.Running++
		case "failed":
			s.Failed++
		}
	}
	return s
}
