// Public HTTPS access: the https-serve worker (systemd-managed) and the
// root-only `access` CLI (status / enable-https / disable-https /
// configure).
//
// Privilege model: https-serve runs unprivileged as drevanta-https with
// CAP_NET_BIND_SERVICE; `drevanta access` runs as root because it
// writes /etc/drevanta/https.json and controls the systemd unit. It
// never opens the SQLite database and never sees claim tokens.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
)

func cmdHTTPServe(args []string) int {
	fs := flag.NewFlagSet("https-serve", flag.ContinueOnError)
	cfgPath := fs.String("config", httpsd.DefaultConfigPath, "HTTPS access config file")
	stateDir := fs.String("state-dir", httpsd.DefaultHTTPStateDir, "certificates and ACME account state")
	overlayDir := fs.String("overlay-dir", httpsd.OverlayDir(), "optional custom-domain overlay directory (empty disables the domain slot)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := httpsd.Load(*cfgPath)
	if err != nil {
		slogError("drevantad https-serve: load config", err)
		return 1
	}
	if !cfg.Enabled {
		slog.Error("drevantad https-serve: config is disabled; nothing to serve")
		return 1
	}
	if err := os.MkdirAll(*stateDir, 0o750); err != nil {
		slogError("drevantad https-serve: state dir", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mgr := httpsd.NewManager(cfg, *stateDir, *overlayDir)
	go mgr.Run(ctx) // certificate lifecycle (issue, renew, record status)

	slog.Info("drevantad https-serve starting", "host", cfg.Host, "staging", cfg.Staging, "profile", cfg.Profile, "overlay_dir", *overlayDir)
	if err := httpsd.Serve(ctx, cfg, mgr); err != nil {
		slogError("drevantad https-serve", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// `drevanta access ...`
// ---------------------------------------------------------------------------

type accessEnv struct {
	configPath string
	stateDir   string
	systemctl  string // "systemctl" or a test-injected script
	isRoot     bool
	stdout     io.Writer
	stderr     io.Writer
}

func newAccessEnv(stdout, stderr io.Writer) accessEnv {
	p := httpsd.RuntimePathsFromEnv()
	sysctl := os.Getenv("DREVANTA_SYSTEMCTL")
	if sysctl == "" {
		sysctl = "systemctl"
	}
	isRoot := os.Geteuid() == 0
	if os.Getenv("DREVANTA_ACCESS_FAKE_ROOT") == "1" { // tests only
		isRoot = true
	}
	return accessEnv{configPath: p.ConfigPath, stateDir: p.StateDir, systemctl: sysctl, isRoot: isRoot, stdout: stdout, stderr: stderr}
}

func cmdAccess(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		accessUsage(stderr)
		return 2
	}
	env := newAccessEnv(stdout, stderr)
	switch args[0] {
	case "status":
		return accessStatus(env, args[1:])
	case "enable-https":
		return accessEnableHTTPS(env, args[1:])
	case "disable-https":
		return accessDisableHTTPS(env, args[1:])
	case "configure":
		return accessConfigure(env, args[1:])
	case "-h", "--help", "help":
		accessUsage(stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "drevanta access: unknown command %q\n", args[0])
		accessUsage(stderr)
		return 2
	}
}

func accessUsage(w io.Writer) {
	fmt.Fprint(w, `usage: drevanta access <status|enable-https|disable-https|configure> [flags]

  status          show the current access mode, HTTPS certificate state,
                  the detected public IP and the SSH-tunnel fallback
  enable-https    obtain a publicly trusted certificate for this VPS's
                  public IP (or a custom domain) and serve HTTPS on
                  ports 80/443 in front of the loopback-only daemon
  disable-https   stop serving public HTTPS; the backend, its data and
                  the SSH-tunnel access remain fully available
  configure       change the HTTPS target host / ACME email / staging
                  endpoint on an existing configuration

enable-https flags:
  --ip ADDRESS      use this public IP (default: detected automatically)
  --domain NAME     issue for a domain you control instead of the IP
  --email ADDRESS   ACME account contact (optional, expiry notices)
  --staging         use the Let's Encrypt staging endpoint (testing)
  --wait DURATION   max time to wait for first issuance (default 120s)

configure flags: --ip/--domain/--email/--staging[=true|false]

Root is required. Public HTTPS is always opt-in: fresh installs attempt
it once, upgrades never enable or disable it on their own.
`)
}

func (e accessEnv) systemctlRun(args ...string) error {
	cmd := exec.Command(e.systemctl, args...)
	cmd.Stdout = e.stdout
	cmd.Stderr = e.stderr
	return cmd.Run()
}

func (e accessEnv) systemdOutput(args ...string) (string, error) {
	cmd := exec.Command(e.systemctl, args...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// ensureRoot refuses non-root unless the test harness injected fakes.
func (e accessEnv) ensureRoot() error {
	if e.isRoot {
		return nil
	}
	if os.Getenv("DREVANTA_SYSTEMCTL") != "" {
		return nil // sandboxed harness: systemd interactions are faked
	}
	return errors.New("drevanta access must run as root (try: sudo drevanta access ...)")
}

// ---------------------------------------------------------------------------
// enable-https
// ---------------------------------------------------------------------------

func accessEnableHTTPS(e accessEnv, args []string) int {
	fs := flag.NewFlagSet("access enable-https", flag.ContinueOnError)
	ipFlag := fs.String("ip", "", "public IP address to issue the certificate for")
	domainFlag := fs.String("domain", "", "custom domain to issue the certificate for")
	email := fs.String("email", "", "optional ACME contact address")
	staging := fs.Bool("staging", false, "use the Let's Encrypt staging endpoint")
	wait := fs.Duration("wait", 120*time.Second, "max wait for the first certificate")
	if err := fs.Parse(args); err != nil {
		accessUsage(e.stderr)
		return 2
	}
	if err := e.ensureRoot(); err != nil {
		fmt.Fprintln(e.stderr, err)
		return 2
	}

	cfg, err := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: e.configPath, StateDir: e.stateDir})
	if err != nil {
		fmt.Fprintf(e.stderr, "reading current config failed: %v\n", err)
		return 1
	}

	host, source, hostErr := resolveAccessHost(*ipFlag, *domainFlag, cfg.Host)
	if hostErr != nil {
		fmt.Fprintf(e.stderr, "%v\n", hostErr)
		fmt.Fprintln(e.stderr, "public HTTPS needs a globally reachable IP or a domain you control;")
		fmt.Fprintln(e.stderr, "pass --ip ADDRESS explicitly or use the SSH-tunnel fallback:")
		fmt.Fprintln(e.stderr, "  ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP")
		return 1
	}

	// Ports: 80/443 in production; test rigs override via environment
	// (mirrors the config's http01_port/tlsalpn_port test overrides).
	httpPort := envPort("DREVANTA_HTTP_PORT", 80)
	httpsPort := envPort("DREVANTA_HTTPS_PORT", 443)
	// Our own proxy unit legitimately holds 80/443 while it is running:
	// an upgrade rewrites the unit and restarts it below, which re-binds
	// the ports. Only a FOREIGN listener is a genuine conflict. (Same
	// precedent as install.sh's port-8080 check, which recognizes the
	// existing drevanta service during upgrades.)
	// systemctl is-active exits 0 iff the unit is active — the exit
	// status is the reliable signal (some environments print nothing).
	_, unitActiveErr := e.systemdOutput("is-active", httpsd.UnitName)
	for _, port := range []int{httpPort, httpsPort} {
		if httpsd.PortFree(port) {
			continue
		}
		if unitActiveErr == nil {
			fmt.Fprintf(e.stdout, "port %d is held by the existing %s service (upgrade); restarting it below re-binds the port\n", port, httpsd.UnitName)
			continue
		}
		fmt.Fprintf(e.stderr, "port %d is already in use by another program — free it and retry\n", port)
		fmt.Fprintln(e.stderr, "hint: ss -tlnp | grep -E ':(80|443)\\b'")
		return 1
	}

	if cfg.Host != host && cfg.Host != "" {
		fmt.Fprintf(e.stdout, "changing HTTPS host: %s -> %s\n", cfg.Host, host)
	}
	newCfg := cfg
	newCfg.Enabled = true
	newCfg.Host = host
	newCfg.HTTPPort = httpPort
	newCfg.HTTPSPort = httpsPort
	if *email != "" {
		newCfg.Email = *email
	}
	if *staging {
		newCfg.Staging = true
	}

	if err := ensureProxyUser(e.stderr); err != nil {
		fmt.Fprintf(e.stderr, "creating the %s service user failed: %v\n", httpsd.ProxyUser, err)
		return 1
	}
	// The custom-domain overlay directory: the daemon (web UI) writes
	// its secret-free domain.json here, the proxy worker reads it.
	// Non-fatal: a missing directory only disables the domain slot.
	if err := ensureOverlayDir(); err != nil {
		fmt.Fprintf(e.stderr, "note: custom-domain overlay dir unavailable (%v) — the dashboard domain step stays disabled\n", err)
	}
	if err := writeProxyUnit(e.configPath, e.stateDir, e.stderr); err != nil {
		fmt.Fprintf(e.stderr, "writing the systemd unit failed: %v\n", err)
		return 1
	}
	if err := newCfg.Save(e.configPath); err != nil {
		fmt.Fprintf(e.stderr, "writing %s failed: %v\n", e.configPath, err)
		return 1
	}

	fmt.Fprintf(e.stdout, "==> HTTPS target: %s (%s)\n", host, source)
	if err := e.systemctlRun("daemon-reload"); err != nil {
		fmt.Fprintf(e.stderr, "systemctl daemon-reload failed: %v\n", err)
		return 1
	}
	if err := e.systemctlRun("enable", "--now", httpsd.UnitName); err != nil {
		fmt.Fprintf(e.stderr, "starting %s failed: %v\n", httpsd.UnitName, err)
		fmt.Fprintln(e.stderr, "check: journalctl -u drevanta-https -n 50")
		return 1
	}

	if st := waitActive(e.stateDir, *wait); st != nil {
		printAccessSuccessScreen(e.stdout, e.stderr, newCfg, *st)
		return 0
	}
	st, _ := httpsd.ReadStatus(e.stateDir)
	fmt.Fprintf(e.stdout, `
✗ PUBLIC HTTPS IS NOT READY YET

  The proxy service is running and keeps retrying in the background.
  Last reported reason: %s

  Typical causes, in order of likelihood:
    1. Inbound TCP 80/443 is blocked (provider firewall / security group)
    2. This VPS is behind NAT and %s is not actually routed to it
    3. Rate limits or a temporary CA outage

  Check progress and diagnosis:
    sudo drevanta access status
    journalctl -u drevanta-https -n 50

  The SSH-tunnel fallback works right now and stays available:
    ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP
`, firstLine(st.LastError), host)
	return 1
}

// envPort reads an integer port from the environment with a default.
func envPort(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return def
}

func firstLine(s string) string {
	if s == "" {
		return "(no error recorded yet — still issuing)"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func waitActive(stateDir string, wait time.Duration) *httpsd.Status {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		st, err := httpsd.ReadStatus(stateDir)
		if err == nil && st.Phase == httpsd.PhaseActive {
			return &st
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

// resolveAccessHost decides the certificate subject. Explicit flags win,
// then the previous configuration, then the SSH session's server
// address, then first-party interface detection. Never guesses a value
// it cannot stand behind: detection failures come back as errors.
func resolveAccessHost(ipFlag, domainFlag, previous string) (host, source string, err error) {
	switch {
	case domainFlag != "":
		if strings.TrimSpace(domainFlag) != domainFlag || strings.ContainsAny(domainFlag, "/\\ \t") {
			return "", "", fmt.Errorf("invalid domain %q", domainFlag)
		}
		return strings.TrimSuffix(domainFlag, "."), "--domain flag", nil
	case ipFlag != "":
		ip := net.ParseIP(strings.TrimSpace(ipFlag))
		if ip == nil {
			return "", "", fmt.Errorf("not an IP address: %q", ipFlag)
		}
		if !httpsd.IsPublicIP(ip) {
			return "", "", fmt.Errorf("%s is not a public IP address (loopback/private/link-local ranges cannot get publicly trusted certificates)", ip)
		}
		return ip.String(), "--ip flag", nil
	case previous != "":
		return previous, "existing configuration", nil
	}
	if ip, _, ok := sshConnectionServerIP(); ok && httpsd.IsPublicIP(ip) {
		return ip.String(), "this SSH session", nil
	}
	if det, ifc := httpsd.DetectPublicIP(); det != "" {
		return det, "default interface " + ifc, nil
	}
	return "", "", errors.New("no public IP address could be detected")
}

// sshConnectionServerIP returns the server address of the operator's
// own SSH session (SSH_CONNECTION field 3) when available.
func sshConnectionServerIP() (net.IP, string, bool) {
	sc := os.Getenv("SSH_CONNECTION")
	if sc == "" {
		return nil, "", false
	}
	f := strings.Fields(sc)
	if len(f) < 3 {
		return nil, "", false
	}
	ip := net.ParseIP(f[2])
	if ip == nil {
		return nil, "", false
	}
	return ip, "SSH_CONNECTION", true
}

// ensureProxyUser creates the dedicated unprivileged service user
// (idempotent). Tests with a fake systemctl skip real useradd via the
// existing user check.
func ensureProxyUser(stderr io.Writer) error {
	if _, err := user.Lookup(httpsd.ProxyUser); err == nil {
		return nil
	}
	if os.Getenv("DREVANTA_SKIP_USERADD") == "1" { // test harness
		return nil
	}
	cmd := exec.Command("useradd", "--system", "--no-create-home",
		"--home-dir", "/var/lib/drevanta-https", "--shell", "/usr/sbin/nologin",
		httpsd.ProxyUser)
	if out, err := cmd.CombinedOutput(); err != nil {
		stderr.Write(out)
		return err
	}
	return nil
}

// ensureOverlayDir creates the shared domain-overlay directory (0755,
// group of the daemon user when it exists) so the unprivileged daemon
// can write the secret-free overlay and the proxy worker can read it.
// Idempotent; safe to call on every enable-https.
func ensureOverlayDir() error {
	dir := httpsd.OverlayDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if u, err := user.Lookup("drevanta"); err == nil {
		if gid, gerr := strconv.Atoi(u.Gid); gerr == nil {
			if uid, uerr := strconv.Atoi(u.Uid); uerr == nil {
				_ = os.Chown(dir, uid, gid)
			}
		}
	}
	return os.Chmod(dir, 0o755)
}

// httpsUnitContent renders the hardened unit. ReadWritePaths is not
// needed: the config file is only read, everything writable lives in
// StateDirectory.
func httpsUnitContent(configPath, stateDir, optDir string) string {
	return fmt.Sprintf(`[Unit]
Description=Drevanta public HTTPS proxy (ACME + reverse proxy)
Documentation=https://github.com/backupamunga-ops/drevanta_server_manager
After=network-online.target drevanta.service
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s/drevantad https-serve --config %s --state-dir %s
Restart=on-failure
RestartSec=10s

# --- Resource limits ---
MemoryMax=128M
MemoryHigh=96M
TasksMax=32
LimitNOFILE=4096

# --- Port binding: the ONLY capability it needs ---
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes

# --- Filesystem isolation ---
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
StateDirectory=%s
ProtectProc=invisible

# --- Kernel surface ---
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
UMask=0027

[Install]
WantedBy=multi-user.target
`, httpsd.ProxyUser, httpsd.ProxyUser, optDir, configPath, stateDir, httpsd.ProxyUser)
}

// writeProxyUnit installs /etc/systemd/system/drevanta-https.service
// (idempotent: only rewrites when content differs).
func writeProxyUnit(configPath, stateDir string, stderr io.Writer) error {
	optDir := "/opt/drevanta/current"
	if v := os.Getenv("DREVANTA_OPT_DIR"); v != "" {
		optDir = v + "/current"
	}
	content := httpsUnitContent(configPath, stateDir, optDir)
	path := "/etc/systemd/system/" + httpsd.UnitName
	if os.Getenv("DREVANTA_SYSTEMD_UNIT_DIR") != "" { // test harness
		path = os.Getenv("DREVANTA_SYSTEMD_UNIT_DIR") + "/" + httpsd.UnitName
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %s\n", path)
	return nil
}

// ---------------------------------------------------------------------------
// status / disable-https / configure
// ---------------------------------------------------------------------------

func accessStatus(e accessEnv, args []string) int {
	fs := flag.NewFlagSet("access status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		accessUsage(e.stderr)
		return 2
	}
	cfg, cfgErr := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: e.configPath, StateDir: e.stateDir})
	st, stErr := httpsd.ReadStatus(e.stateDir)

	unitState, unitErr := e.systemdOutput("is-active", httpsd.UnitName)
	if unitErr != nil {
		unitState = "inactive"
	}

	fmt.Fprintln(e.stdout, "ACCESS MODE")
	httpsOn := cfgErr == nil && cfg.Enabled && unitState == "active"
	if httpsOn {
		scheme := "https"
		fmt.Fprintf(e.stdout, "  public HTTPS:  ENABLED  (%s://%s)\n", scheme, cfg.Host)
		if cfg.Staging {
			fmt.Fprintln(e.stdout, "  certificate:   Let's Encrypt STAGING (not trusted by browsers)")
		}
	} else {
		fmt.Fprintln(e.stdout, "  public HTTPS:  disabled")
		fmt.Fprintln(e.stdout, "  private access: SSH tunnel (always available)")
	}

	ip, ifc := httpsd.DetectPublicIP()
	fmt.Fprintln(e.stdout, "\nDETECTED ADDRESS")
	if ip != "" {
		fmt.Fprintf(e.stdout, "  public IP:     %s  (interface %s)\n", ip, ifc)
	} else {
		fmt.Fprintln(e.stdout, "  public IP:     none detected — pass --ip to enable-https")
	}
	if sip, _, ok := sshConnectionServerIP(); ok {
		fmt.Fprintf(e.stdout, "  this session:  server address %s\n", sip)
	}

	if cfgErr == nil && cfg.Enabled {
		fmt.Fprintf(e.stdout, "\nHTTPS CONFIGURATION\n")
		fmt.Fprintf(e.stdout, "  target host:   %s\n", cfg.Host)
		fmt.Fprintf(e.stdout, "  ACME profile:  %s\n", cfg.Profile)
		if cfg.Staging {
			fmt.Fprintln(e.stdout, "  endpoint:      Let's Encrypt staging")
		}
		fmt.Fprintf(e.stdout, "  backend:       %s (loopback-only daemon)\n", cfg.Backend)
	}

	fmt.Fprintln(e.stdout, "\nCERTIFICATE")
	switch {
	case stErr != nil:
		fmt.Fprintf(e.stdout, "  status file unreadable: %v\n", stErr)
	case st.Phase == "":
		fmt.Fprintln(e.stdout, "  never issued (proxy has not run yet)")
	case st.Phase == httpsd.PhaseActive || st.Phase == httpsd.PhaseRenewing:
		fmt.Fprintf(e.stdout, "  status:        %s\n", st.Phase)
		fmt.Fprintf(e.stdout, "  subject:       %s\n", st.Host)
		fmt.Fprintf(e.stdout, "  valid until:   %s\n", st.NotAfter.Local().Format("2006-01-02 15:04 MST"))
		fmt.Fprintf(e.stdout, "  renewals:      %d\n", st.Renewals)
		if !st.LastErrorAt.IsZero() {
			fmt.Fprintf(e.stdout, "  last error:    %s (at %s)\n", firstLine(st.LastError), st.LastErrorAt.Local().Format("2006-01-02 15:04"))
		}
	default:
		fmt.Fprintf(e.stdout, "  status:        %s\n", st.Phase)
		if st.LastError != "" {
			fmt.Fprintf(e.stdout, "  last error:    %s\n", firstLine(st.LastError))
		}
	}

	if ds := st.Domain; ds != nil && ds.Host != "" {
		fmt.Fprintln(e.stdout, "\nCUSTOM DOMAIN (secondary certificate slot; the IP certificate above keeps serving)")
		fmt.Fprintf(e.stdout, "  domain:        %s\n", ds.Host)
		fmt.Fprintf(e.stdout, "  status:        %s\n", ds.Phase)
		if !ds.NotAfter.IsZero() && (ds.Phase == httpsd.PhaseActive || ds.Phase == httpsd.PhaseRenewing) {
			fmt.Fprintf(e.stdout, "  valid until:   %s\n", ds.NotAfter.Local().Format("2006-01-02 15:04 MST"))
		}
		if ds.LastError != "" {
			fmt.Fprintf(e.stdout, "  last error:    %s\n", firstLine(ds.LastError))
		}
		fmt.Fprintln(e.stdout, "  manage in the dashboard under Settings, or: sudo drevanta access configure")
	}

	fmt.Fprintln(e.stdout, "\nSSH-TUNNEL FALLBACK (works regardless of HTTPS)")
	fmt.Fprintln(e.stdout, "  ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP")
	fmt.Fprintln(e.stdout, "  then open http://127.0.0.1:18080/")
	return 0
}

func accessDisableHTTPS(e accessEnv, args []string) int {
	fs := flag.NewFlagSet("access disable-https", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		accessUsage(e.stderr)
		return 2
	}
	if err := e.ensureRoot(); err != nil {
		fmt.Fprintln(e.stderr, err)
		return 2
	}
	cfg, _ := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: e.configPath, StateDir: e.stateDir})
	if cfg.Enabled {
		cfg.Enabled = false
		if err := cfg.Save(e.configPath); err != nil {
			fmt.Fprintf(e.stderr, "updating %s failed: %v\n", e.configPath, err)
			return 1
		}
	}
	if err := e.systemctlRun("disable", "--now", httpsd.UnitName); err != nil {
		fmt.Fprintf(e.stderr, "stopping %s failed: %v\n", httpsd.UnitName, err)
		return 1
	}
	fmt.Fprint(e.stdout, `
✓ PUBLIC HTTPS DISABLED

  The Drevanta backend and all data are untouched and keep running.
  Certificates were kept for a fast re-enable.

  Private access (SSH tunnel):
    ssh -L 18080:127.0.0.1:8080 USER@YOUR_VPS_IP
    then open http://127.0.0.1:18080/

  Re-enable later: sudo drevanta access enable-https
`)
	return 0
}

func accessConfigure(e accessEnv, args []string) int {
	fs := flag.NewFlagSet("access configure", flag.ContinueOnError)
	ipFlag := fs.String("ip", "", "new public IP target")
	domainFlag := fs.String("domain", "", "new custom-domain target")
	email := fs.String("email", "", "ACME contact address")
	staging := fs.String("staging", "", "true/false: Let's Encrypt staging endpoint")
	if err := fs.Parse(args); err != nil {
		accessUsage(e.stderr)
		return 2
	}
	if err := e.ensureRoot(); err != nil {
		fmt.Fprintln(e.stderr, err)
		return 2
	}
	cfg, err := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: e.configPath, StateDir: e.stateDir})
	if err != nil {
		fmt.Fprintf(e.stderr, "reading config: %v\n", err)
		return 1
	}
	if !cfg.Enabled && *ipFlag == "" && *domainFlag == "" && *email == "" && *staging == "" {
		fmt.Fprintln(e.stdout, "HTTPS is not configured yet. Set it up with:")
		fmt.Fprintln(e.stdout, "  sudo drevanta access enable-https")
		return 0
	}
	changed := false
	if *ipFlag != "" {
		ip := net.ParseIP(strings.TrimSpace(*ipFlag))
		if ip == nil || !httpsd.IsPublicIP(ip) {
			fmt.Fprintf(e.stderr, "%q is not a public IP address\n", *ipFlag)
			return 1
		}
		cfg.Host = ip.String()
		cfg.Staging = false
		changed = true
	}
	if *domainFlag != "" {
		cfg.Host = strings.TrimSuffix(*domainFlag, ".")
		changed = true
	}
	if *email != "" {
		cfg.Email = *email
		changed = true
	}
	if *staging != "" {
		b, perr := strconv.ParseBool(*staging)
		if perr != nil {
			fmt.Fprintln(e.stderr, "--staging takes true or false")
			return 2
		}
		cfg.Staging = b
		changed = true
	}
	if !changed {
		fmt.Fprintln(e.stdout, "Nothing to change. Current configuration:")
		fmt.Fprintln(e.stdout, "  host:    "+cfg.Host)
		fmt.Fprintln(e.stdout, "  email:   "+cfg.Email)
		fmt.Fprintln(e.stdout, "  staging: "+fmt.Sprint(cfg.Staging))
		return 0
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(e.stderr, "resulting config invalid: %v\n", err)
		return 1
	}
	if err := cfg.Save(e.configPath); err != nil {
		fmt.Fprintf(e.stderr, "writing config: %v\n", err)
		return 1
	}
	if unitState, uerr := e.systemdOutput("is-active", httpsd.UnitName); uerr == nil && unitState == "active" {
		if err := e.systemctlRun("restart", httpsd.UnitName); err != nil {
			fmt.Fprintf(e.stderr, "restarting the proxy failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(e.stdout, "HTTPS target updated; proxy restarted and re-issuing.")
	} else {
		fmt.Fprintln(e.stdout, "Configuration saved. Start serving with:")
		fmt.Fprintln(e.stdout, "  sudo drevanta access enable-https")
	}
	return 0
}

// printAccessSuccessScreen is the enable-https success screen (the
// installer renders its own variant with the claim token embedded).
func printAccessSuccessScreen(stdout, stderr io.Writer, cfg httpsd.Config, st httpsd.Status) {
	fmt.Fprintf(stdout, `
✓ PUBLIC HTTPS IS LIVE

  Open Drevanta in your browser:

    https://%s/

  Certificate: publicly trusted%s
  Valid until: %s (renews automatically)

  The Drevanta daemon itself stays loopback-only.
  Disable public HTTPS any time: sudo drevanta access disable-https
`, cfg.Host, stagingSuffix(cfg), st.NotAfter.Local().Format("2006-01-02 15:04 MST"))
	_ = stderr
}

func stagingSuffix(cfg httpsd.Config) string {
	if cfg.Staging {
		return " (Let's Encrypt STAGING — browsers will warn)"
	}
	return ""
}

// marshalForTest is used by the unit tests of the access config flows.
func marshalForTest(c httpsd.Config) string {
	b, _ := json.Marshal(c)
	return string(b)
}
