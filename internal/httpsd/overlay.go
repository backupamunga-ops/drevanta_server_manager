// Optional custom-domain overlay: a single optional DNS-name
// certificate slot next to the primary IP certificate.
//
// Model: the certificate for cfg.Host (the VPS public IP) is ALWAYS the
// primary slot — it can never be displaced by a domain operation, so
// IP access keeps working even when a domain fails to issue. The
// optional domain is a second, independent slot.
//
// The overlay file lives in a daemon-writable, proxy-readable directory
// (/var/lib/drevanta-access, override DREVANTA_ACCESS_OVERLAY_DIR) so
// the unprivileged web UI can manage it through the daemon API without
// root and without touching the root-owned /etc/drevanta/https.json.
// The file is 0644 and secret-free; the proxy worker only ever reads it.
package httpsd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// DefaultOverlayDir is the shared domain-overlay directory. The daemon
// (user drevanta) writes it; the httpsd proxy worker (user
// drevanta-https) reads it. Both only handle secret-free JSON.
const DefaultOverlayDir = "/var/lib/drevanta-access"

const overlayFileName = "domain.json"

// OverlayDir resolves the overlay directory (env override for tests).
func OverlayDir() string {
	if v := os.Getenv("DREVANTA_ACCESS_OVERLAY_DIR"); v != "" {
		return v
	}
	return DefaultOverlayDir
}

// OverlayPath is the overlay file location inside dir.
func OverlayPath(dir string) string {
	return filepath.Join(dir, overlayFileName)
}

// DomainOverlay is the on-disk overlay document. An empty Domain (or a
// missing file) means "no custom domain".
type DomainOverlay struct {
	Domain string `json:"domain"`
}

// LoadDomainOverlay reads the overlay; a missing file yields an empty
// overlay (no domain), not an error.
func LoadDomainOverlay(dir string) (DomainOverlay, error) {
	raw, err := os.ReadFile(OverlayPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return DomainOverlay{}, nil
		}
		return DomainOverlay{}, fmt.Errorf("read overlay: %w", err)
	}
	var o DomainOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return DomainOverlay{}, fmt.Errorf("parse overlay: %w", err)
	}
	o.Domain = NormalizeDomain(o.Domain)
	return o, nil
}

// SaveDomainOverlay atomically writes the overlay. The file MUST end up
// 0644 (secret-free, readable by the proxy worker's user): both service
// units run UMask=0027, so a plain 0644 argument would land as 0640 and
// the worker's user could not open the file — provisioning silently
// never started. The explicit Chmod pins the mode regardless of umask.
// An empty domain removes the file.
func SaveDomainOverlay(dir, domain string) error {
	domain = NormalizeDomain(domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("overlay dir: %w", err)
	}
	path := OverlayPath(dir)
	if domain == "" {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove overlay: %w", err)
		}
		return nil
	}
	raw, err := json.MarshalIndent(DomainOverlay{Domain: domain}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write overlay: %w", err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("chmod overlay: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace overlay: %w", err)
	}
	// Belt and braces: pin the final file too (e.g. if a previous
	// umask-affected 0640 file was replaced).
	return os.Chmod(path, 0o644)
}

// NormalizeDomain lowercases, trims spaces and one trailing dot.
func NormalizeDomain(d string) string {
	d = strings.TrimSpace(strings.ToLower(d))
	d = strings.TrimSuffix(d, ".")
	return d
}

// ValidHostname enforces the shape of a certifiable DNS name: 1-253
// octets, ASCII letters/digits/hyphen labels that do not start or end
// with a hyphen, no wildcard, no underscore, and not an IP literal
// (IP subjects belong to the primary slot). It deliberately does NOT
// resolve the name — that is the separate DNS check.
func ValidHostname(d string) error {
	d = NormalizeDomain(d)
	if d == "" {
		return fmt.Errorf("domain is empty")
	}
	if len(d) > 253 {
		return fmt.Errorf("domain is longer than 253 characters")
	}
	if net.ParseIP(d) != nil {
		return fmt.Errorf("%q is an IP address — the server IP is already the default access address", d)
	}
	if strings.Contains(d, "*") {
		return fmt.Errorf("wildcard certificates are not supported")
	}
	if strings.ContainsAny(d, " /\\ \t@:_,") {
		return fmt.Errorf("domain must be a bare hostname like vpn.example.com")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain needs at least two labels (like example.com)")
	}
	for _, l := range labels {
		if l == "" {
			return fmt.Errorf("domain has an empty label (check the dots)")
		}
		if len(l) > 63 {
			return fmt.Errorf("domain label %q is longer than 63 characters", l)
		}
		for i, r := range l {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(l)-1)
			if !ok {
				return fmt.Errorf("domain label %q contains invalid characters (use a-z, 0-9 and interior hyphens)", l)
			}
		}
	}
	return nil
}
