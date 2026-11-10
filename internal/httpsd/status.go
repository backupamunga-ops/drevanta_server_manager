package httpsd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Status phases (state machine of the httpsd process).
const (
	PhaseStarting = "starting" // config loaded, no certificate yet
	PhaseIssuing  = "issuing"  // ACME order in progress
	PhaseActive   = "active"   // valid certificate being served
	PhaseRenewing = "renewing" // serving an old cert while renewing
	PhaseError    = "error"    // issuance failed; see LastError
	PhaseDisabled = "disabled" // config disabled or unit stopped
)

// Status is the secret-free state file the httpsd process rewrites on
// every transition. The `drevanta access` CLI, the claim CLI and the
// installer read it; nothing in it can be used to authenticate.
//
// 0644 on purpose: it lives in the httpsd state directory and helps the
// unprivileged drevanta user (claim screen) report the access mode.
type Status struct {
	Phase       string        `json:"phase"`
	Host        string        `json:"host"`
	Profile     string        `json:"profile"`
	Staging     bool          `json:"staging"`
	NotBefore   time.Time     `json:"not_before,omitempty"`
	NotAfter    time.Time     `json:"not_after,omitempty"`
	IssuedAt    time.Time     `json:"issued_at,omitempty"`
	Renewals    int           `json:"renewals"`
	LastError   string        `json:"last_error,omitempty"`
	LastErrorAt time.Time     `json:"last_error_at,omitempty"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Domain      *DomainStatus `json:"domain,omitempty"`
}

// DomainStatus is the secret-free state of the OPTIONAL custom-domain
// certificate slot. It never affects the primary (IP) slot: the top
// level Status fields always describe the primary certificate.
type DomainStatus struct {
	Host      string    `json:"host"`
	Phase     string    `json:"phase"`
	NotBefore time.Time `json:"not_before,omitempty"`
	NotAfter  time.Time `json:"not_after,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CertValid reports whether the domain slot describes a currently valid
// certificate (same margins as Status.CertValid).
func (d DomainStatus) CertValid(now time.Time) bool {
	if d.Phase != PhaseActive && d.Phase != PhaseRenewing {
		return false
	}
	if d.NotAfter.IsZero() {
		return false
	}
	return now.Before(d.NotAfter.Add(-30 * time.Second))
}

// StatusPath is the canonical status file location inside the state dir.
func StatusPath(stateDir string) string { return filepath.Join(stateDir, "status.json") }

// WriteStatus atomically persists st (0644, no secrets).
func WriteStatus(stateDir string, st Status) error {
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	tmp := StatusPath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, StatusPath(stateDir))
}

// ReadStatus loads the status file; a missing file yields a zero Status
// and no error (callers treat that as "never started").
func ReadStatus(stateDir string) (Status, error) {
	raw, err := os.ReadFile(StatusPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Status{}, nil
		}
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, err
	}
	return st, nil
}

// CertValid reports whether the status describes a currently valid
// certificate (with a small safety margin).
func (s Status) CertValid(now time.Time) bool {
	if s.Phase != PhaseActive && s.Phase != PhaseRenewing {
		return false
	}
	if s.NotAfter.IsZero() {
		return false
	}
	return now.Before(s.NotAfter.Add(-30 * time.Second))
}
