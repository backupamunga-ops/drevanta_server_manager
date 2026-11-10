// Package httpsd is Drevanta's public HTTPS access layer.
//
// It runs as a separate unprivileged process (systemd unit
// drevanta-https.service, user drevanta-https, CAP_NET_BIND_SERVICE) in
// front of the loopback-only drevanta daemon:
//
//	browser --HTTPS :443--> httpsd --HTTP--> 127.0.0.1:8080 (drevantad)
//	           :80  = ACME http-01 + redirect to :443
//
// Responsibilities:
//   - obtain and auto-renew a publicly trusted certificate for the VPS
//     public IP address (Let's Encrypt "shortlived" profile, RFC 8738 IP
//     identifiers) or for an optional custom domain, via the maintained
//     lego ACME client library;
//   - terminate TLS and reverse-proxy to the private backend with a
//     strict trusted-proxy boundary (client-supplied forwarding headers
//     are stripped, X-Forwarded-Proto/For are set here and only here);
//   - never expose the backend over public plain HTTP (port 80 serves
//     only the ACME challenge and redirects everything else);
//   - publish a secret-free status file for the `drevanta access` CLI
//     and the installer.
//
// The daemon itself (drevantad serve) keeps binding 127.0.0.1 only.
package httpsd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Default ACME endpoints and profile. IP address certificates must use a
// short-lived profile (Let's Encrypt: ~160 hours) — see RFC 8738 and the
// ACME profiles extension.
const (
	DefaultACMEDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	StagingACMEDirectory = "https://staging-v02.api.letsencrypt.org/directory"
	DefaultProfile       = "shortlived"
)

// Config is the on-disk HTTPS access configuration. It contains no
// secrets (the ACME account key and certificate private key live in the
// httpsd state directory), so the file is world-readable and the claim
// CLI can use it to choose the right setup-link format.
type Config struct {
	Enabled bool `json:"enabled"`

	// Host is the certificate subject: a public IP address (the primary
	// locked access model) or an optional custom domain.
	Host string `json:"host"`

	HTTPPort    int    `json:"http_port"`       // 0 = 80
	HTTPSPort   int    `json:"https_port"`      // 0 = 443
	Backend     string `json:"backend"`         // "" = 127.0.0.1:8080
	BindIP      string `json:"bind_ip"`         // "" = all interfaces
	Staging     bool   `json:"staging"`         // LE staging endpoint (testing)
	Profile     string `json:"profile"`         // "" = shortlived
	Email       string `json:"email,omitempty"` // optional ACME contact
	ACMEDir     string `json:"acme_directory"`  // "" = production or staging
	CARoot      string `json:"acme_ca_root"`    // optional path to a CA PEM (test rigs)
	HTTP01Port  int    `json:"http01_port"`     // test override; 0 = HTTPPort
	TLSALPNPort int    `json:"tlsalpn_port"`    // test override; 0 = HTTPSPort
}

// Default returns the production configuration with host unset.
func Default() Config {
	return Config{
		Enabled:   true,
		HTTPPort:  80,
		HTTPSPort: 443,
		Backend:   "127.0.0.1:8080",
		Profile:   DefaultProfile,
		ACMEDir:   DefaultACMEDirectory,
	}
}

// Load reads and validates a config file.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config (0644: no secrets) atomically.
func (c Config) Save(path string) error {
	c.applyDefaults()
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.HTTPPort == 0 {
		c.HTTPPort = d.HTTPPort
	}
	if c.HTTPSPort == 0 {
		c.HTTPSPort = d.HTTPSPort
	}
	if c.Backend == "" {
		c.Backend = d.Backend
	}
	if c.Profile == "" {
		c.Profile = d.Profile
	}
	if c.Staging {
		c.ACMEDir = StagingACMEDirectory
	} else if c.ACMEDir == "" {
		c.ACMEDir = d.ACMEDir
	}
}

// Validate enforces the invariants the proxy depends on. It is called on
// every load so a hand-edited config cannot weaken the posture.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil // a disabled config carries no obligations
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required (public IP or domain)")
	}
	if c.HTTPPort <= 0 || c.HTTPPort > 65535 || c.HTTPSPort <= 0 || c.HTTPSPort > 65535 {
		return errors.New("ports must be within 1..65535")
	}
	if c.HTTPPort == c.HTTPSPort {
		return errors.New("http_port and https_port must differ")
	}
	if c.Backend == "" {
		return errors.New("backend is required")
	}
	if !strings.HasPrefix(c.ACMEDir, "https://") {
		return errors.New("acme_directory must be HTTPS")
	}
	if strings.ContainsAny(c.Host, "/\\ \t") {
		return errors.New("host must be a bare IP address or hostname")
	}
	return nil
}

// EffectiveHTTP01Port / EffectiveTLSALPNPort resolve the test overrides.
func (c Config) EffectiveHTTP01Port() int {
	if c.HTTP01Port != 0 {
		return c.HTTP01Port
	}
	return c.HTTPPort
}

func (c Config) EffectiveTLSALPNPort() int {
	if c.TLSALPNPort != 0 {
		return c.TLSALPNPort
	}
	return c.HTTPSPort
}
