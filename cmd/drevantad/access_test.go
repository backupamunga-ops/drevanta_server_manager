package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
)

func TestResolveAccessHost(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	cases := []struct {
		name     string
		ipFlag   string
		domain   string
		previous string
		want     string
		wantErr  bool
	}{
		{"explicit ip", "23.20.15.8", "", "", "23.20.15.8", false},
		{"explicit domain", "", "console.example.com", "", "console.example.com", false},
		{"trailing dot domain", "", "console.example.com.", "", "console.example.com", false},
		{"previous config wins when no flags", "", "", "45.83.90.12", "45.83.90.12", false},
		{"garbage ip", "999.1.2.3", "", "", "", true},
		{"private ip refused", "192.168.1.10", "", "", "", true},
		{"loopback refused", "127.0.0.1", "", "", "", true},
	}
	for _, tc := range cases {
		got, _, err := resolveAccessHost(tc.ipFlag, tc.domain, tc.previous)
		if tc.wantErr != (err != nil) {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("%s: host = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolveAccessHostFromSSHConnection(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "198.51.100.9 51000 23.20.15.8 22")
	got, source, err := resolveAccessHost("", "", "")
	if err != nil || got != "23.20.15.8" {
		t.Fatalf("host = %q err = %v (source %q)", got, err, source)
	}
	if source != "this SSH session" {
		t.Fatalf("source = %q", source)
	}
}

func TestIsPublicIP(t *testing.T) {
	public := []string{"23.20.15.8", "8.8.8.8", "2606:4700::1111"}
	private := []string{
		"127.0.0.1", "::1", "10.0.0.5", "192.168.1.1", "172.16.0.9",
		"169.254.1.1", "100.64.0.1", "0.0.0.0", "224.0.0.1",
	}
	for _, s := range public {
		if !httpsd.IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range private {
		if httpsd.IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should NOT be public", s)
		}
	}
}

func TestConfigRoundTripAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "https.json")
	cfg := httpsd.Config{Enabled: true, Host: "203.0.113.5", Staging: true}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := httpsd.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "203.0.113.5" || !got.Staging || got.ACMEDir != httpsd.StagingACMEDirectory {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Backend != "127.0.0.1:8080" || got.HTTPPort != 80 || got.HTTPSPort != 443 {
		t.Fatalf("defaults not applied: %+v", got)
	}

	// Missing file loads as disabled (fresh machine).
	empty, err := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: filepath.Join(dir, "nope.json")})
	if err != nil || empty.Enabled {
		t.Fatalf("missing config should load disabled, got %+v err %v", empty, err)
	}

	// Invalid host refused.
	bad := httpsd.Default()
	bad.Host = "bad host with spaces"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid host accepted")
	}
}

func TestHTTPSUnitContentShape(t *testing.T) {
	c := httpsUnitContent("/etc/drevanta/https.json", "/var/lib/drevanta-https", "/opt/drevanta")
	for _, want := range []string{
		"User=drevanta-https",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"ProtectSystem=strict",
		"StateDirectory=drevanta-https",
		"ExecStart=/opt/drevanta/drevantad https-serve --config /etc/drevanta/https.json",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
	} {
		if !containsSub(c, want) {
			t.Errorf("unit missing %q", want)
		}
	}
}

func TestWriteProxyUnitIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DREVANTA_SYSTEMD_UNIT_DIR", dir)
	t.Setenv("DREVANTA_OPT_DIR", "/opt/drevanta")
	if err := writeProxyUnit("/etc/drevanta/https.json", "/var/lib/drevanta-https", os.Stderr); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, httpsd.UnitName)
	st1, err := os.Stat(p)
	if err != nil || st1.Mode().Perm().String() != "-rw-r--r--" {
		t.Fatalf("unit perms = %v", st1.Mode())
	}
	if err := writeProxyUnit("/etc/drevanta/https.json", "/var/lib/drevanta-https", os.Stderr); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(p)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("identical unit was rewritten")
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
