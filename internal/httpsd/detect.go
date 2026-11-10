package httpsd

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Well-known runtime locations. Tests override them with environment
// variables; production uses these defaults.
const (
	DefaultConfigPath   = "/etc/drevanta/https.json"
	DefaultHTTPStateDir = "/var/lib/drevanta-https"
	UnitName            = "drevanta-https.service"
	ProxyUser           = "drevanta-https"
)

// RuntimePaths resolves the effective paths (env overrides for tests).
type RuntimePaths struct {
	ConfigPath string
	StateDir   string
}

func RuntimePathsFromEnv() RuntimePaths {
	p := RuntimePaths{ConfigPath: DefaultConfigPath, StateDir: DefaultHTTPStateDir}
	if v := os.Getenv("DREVANTA_ACCESS_CONFIG"); v != "" {
		p.ConfigPath = v
	}
	if v := os.Getenv("DREVANTA_HTTPS_STATE_DIR"); v != "" {
		p.StateDir = v
	}
	return p
}

// LoadRuntimeConfig loads the HTTPS access configuration, or returns a
// disabled zero config when no file exists (fresh machine).
func LoadRuntimeConfig(p RuntimePaths) (Config, error) {
	c, err := Load(p.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{Enabled: false}, nil
		}
		return Config{}, err
	}
	return c, nil
}

// IsPublicIP reports whether ip is a globally routable address (neither
// loopback, private, link-local, nor otherwise special). IPv4-mapped
// IPv6 forms are unwrapped first.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// Shared/NAT64/other special ranges (RFC 6598 100.64/10, benchmark
	// 198.18/15, documentation ranges).
	for _, cidr := range []string{
		"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24",
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
		"fe80::/10", "fc00::/7",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return false
		}
	}
	return true
}

// DefaultRouteInterface parses /proc/net/route for the interface of the
// IPv4 default route (destination 0.0.0.0). "" when unknown.
func DefaultRouteInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		if fields[1] == "00000000" && fields[7] != "00000000" {
			return fields[0]
		}
	}
	return ""
}

// DetectPublicIP returns a candidate public IP address from first-party
// sources only: the default route's interface address first, then any
// global unicast address on any interface. Returns ("", "") when nothing
// public-looking exists; the caller must then ask for an explicit
// --ip/--domain or fall back to the SSH tunnel.
func DetectPublicIP() (ip string, iface string) {
	preferred := DefaultRouteInterface()
	var fallback string
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			naive := ipNet.IP
			v4 := naive.To4()
			if v4 == nil {
				continue // IPv6 handled by its own rules below
			}
			if IsPublicIP(naive) {
				if ifc.Name == preferred {
					return v4.String(), ifc.Name
				}
				if fallback == "" {
					fallback = v4.String()
					iface = ifc.Name
				}
			}
		}
	}
	return fallback, iface
}

// PortFree reports whether a TCP port can be bound on all interfaces
// (and closes the probe socket immediately).
func PortFree(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// StateDirFile is a small helper for tests and the CLI.
func StateDirFile(stateDir, name string) string {
	return filepath.Join(stateDir, name)
}
