package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/usage"
)

type capacity struct{ Used, Total int64 }
type hostUsage struct {
	Host            string
	Cores           int
	CPU             *float64
	RAM, Swap, Disk *capacity
	Uptime          int64
}
type serviceCheck struct {
	Name, State, Detail, Since string
	Restarts                   int
}
type tunnelLocal struct {
	ID    string
	Edges *int
	Ready bool
}

type tunnelRoute struct {
	Hostname string
	Origins  []tunnelOrigin
	Web      bool // An HTTP(S) ingress rule matches the hostname without a path constraint.
}

type tunnelOrigin struct {
	Kind    string
	Address string `json:",omitempty"`
}

// Keep only the protocol and loopback authority. Never forward origin credentials,
// paths, socket filenames, remote hostnames or query strings to the browser.
func describeTunnelOrigin(service string) tunnelOrigin {
	switch {
	case strings.HasPrefix(service, "unix:"), strings.HasPrefix(service, "unix+tls:"):
		return tunnelOrigin{Kind: "socket"}
	case strings.HasPrefix(service, "http_status:"), service == "hello_world", service == "hello-world":
		return tunnelOrigin{Kind: "built-in"}
	}
	u, err := url.Parse(service)
	if err != nil || u.Hostname() == "" {
		return tunnelOrigin{Kind: "unknown"}
	}
	switch u.Scheme {
	case "http", "https", "ssh", "tcp", "rdp", "smb":
	default:
		return tunnelOrigin{Kind: "unknown"}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	ip, _ := netip.ParseAddr(host)
	if host != "localhost" && !(ip.Unmap().IsLoopback() && ip.Zone() == "") {
		return tunnelOrigin{Kind: "network"}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return tunnelOrigin{Kind: "unknown"}
		}
		host = net.JoinHostPort(host, port)
	} else if ip.Is6() {
		host = "[" + host + "]"
	}
	return tunnelOrigin{Kind: "loopback", Address: u.Scheme + "://" + host}
}

var routeHostname = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func localTunnelRoutes(ctx context.Context) []tunnelRoute {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:20241/config", nil)
	resp, err := localProbeClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	return parseTunnelRoutes(resp.Body)
}

// Group ingress rules by published hostname, keeping the distinct sanitized origins for each.
func parseTunnelRoutes(body io.Reader) []tunnelRoute {
	var snapshot struct {
		Config struct {
			Ingress []struct{ Hostname, Path, Service string }
		}
	}
	const maxConfigBytes = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxConfigBytes+1))
	if err != nil || len(raw) > maxConfigBytes || json.Unmarshal(raw, &snapshot) != nil || snapshot.Config.Ingress == nil {
		return nil
	}
	routes := make([]tunnelRoute, 0)
	index := make(map[string]int)
	for _, rule := range snapshot.Config.Ingress {
		host := strings.ToLower(strings.TrimSuffix(rule.Hostname, "."))
		if len(host) > 253 || !routeHostname.MatchString(host) {
			continue
		}
		web := rule.Path == "" && !strings.HasPrefix(host, "*.") && (strings.HasPrefix(rule.Service, "https://") || strings.HasPrefix(rule.Service, "http://"))
		i, exists := index[host]
		if !exists {
			i = len(routes)
			index[host] = i
			routes = append(routes, tunnelRoute{Hostname: host})
		}
		routes[i].Web = routes[i].Web || web
		origin := describeTunnelOrigin(rule.Service)
		if !slices.Contains(routes[i].Origins, origin) {
			routes[i].Origins = append(routes[i].Origins, origin)
		}
	}
	return routes
}

type peer struct {
	IP, Gateway string
	Sockets     int
	LastSeen    time.Time
	Unexpected  bool
}
type accountCheck struct {
	ID, Name, Provider, State string
	At                        time.Time
}
type monitorGateway struct{ unit, port string }

func gateways() []monitorGateway {
	out := []monitorGateway{{"ccm-models", "17879"}}
	if os.Getenv("CCM_MONITOR_STAGING") == "1" {
		out = append(out, monitorGateway{"ccm-staging", "17878"})
	}
	return out
}

func localCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	data, err := c.Output()
	return string(data), err
}
func services(ctx context.Context) []serviceCheck {
	names := []string{"cloudflared", "hasoft-sso", "caddy", "ccm-admin", "ccm-monitor"}
	for _, g := range gateways() {
		names = append(names, g.unit)
	}
	args := []string{"show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,ActiveEnterTimestamp,NRestarts"}
	for _, n := range names {
		args = append(args, n+".service")
	}
	data, _ := localCommand(ctx, "systemctl", args...)
	byName := map[string]serviceCheck{}
	for _, block := range strings.Split(strings.TrimSpace(data), "\n\n") {
		p := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				p[k] = v
			}
		}
		name := strings.TrimSuffix(p["Id"], ".service")
		state := p["ActiveState"]
		if p["LoadState"] != "loaded" {
			state = "unknown"
		}
		restarts, _ := strconv.Atoi(p["NRestarts"])
		byName[name] = serviceCheck{name, state, p["SubState"], p["ActiveEnterTimestamp"], restarts}
	}
	out := make([]serviceCheck, 0, len(names))
	for _, name := range names {
		c, ok := byName[name]
		if !ok {
			c = serviceCheck{Name: name, State: "unknown"}
		}
		out = append(out, c)
	}
	return out
}

var localProbeClient = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func tunnelProbe(ctx context.Context) tunnelLocal {
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:20241/ready", nil)
	resp, err := localProbeClient.Do(r)
	if err != nil {
		return tunnelLocal{}
	}
	defer resp.Body.Close()
	var b struct {
		ConnectorID      string
		ReadyConnections *int
	}
	if json.NewDecoder(resp.Body).Decode(&b) != nil {
		return tunnelLocal{}
	}
	return tunnelLocal{b.ConnectorID, b.ReadyConnections, resp.StatusCode == 200 && b.ReadyConnections != nil && *b.ReadyConnections > 0}
}

type cpuSample struct{ total, idle int64 }

func cpuCounters(data string) (cpuSample, int) {
	var s cpuSample
	cores := 0
	for _, line := range strings.Split(data, "\n") {
		v := strings.Fields(line)
		if len(v) < 5 {
			continue
		}
		if v[0] == "cpu" {
			for i := 1; i < len(v) && i <= 8; i++ {
				n, e := strconv.ParseInt(v[i], 10, 64)
				if e != nil {
					return cpuSample{}, 0
				}
				s.total += n
				if i == 4 || i == 5 {
					s.idle += n
				}
			}
		} else if strings.HasPrefix(v[0], "cpu") {
			cores++
		}
	}
	return s, cores
}
func capacityFrom(total, free int64) *capacity {
	if total < 0 || free < 0 || free > total {
		return nil
	}
	return &capacity{total - free, total}
}
func resources(ctx context.Context, prev *cpuSample) hostUsage {
	host, _ := os.Hostname()
	out := hostUsage{Host: host}
	raw, _ := os.ReadFile("/proc/stat")
	cur, cores := cpuCounters(string(raw))
	out.Cores = cores
	if diff := cur.total - prev.total; prev.total > 0 && diff > 0 && cur.idle >= prev.idle && cur.idle-prev.idle <= diff {
		v := 100 * float64(diff-(cur.idle-prev.idle)) / float64(diff)
		out.CPU = &v
	}
	*prev = cur
	raw, _ = os.ReadFile("/proc/meminfo")
	m := map[string]int64{}
	for _, line := range strings.Split(string(raw), "\n") {
		v := strings.Fields(line)
		if len(v) != 3 || v[2] != "kB" {
			continue
		}
		n, e := strconv.ParseInt(v[1], 10, 64)
		if e == nil {
			m[v[0]] = n * 1024
		}
	}
	mem := func(t, f string) *capacity {
		total, ok := m[t]
		free, ok2 := m[f]
		if !ok || !ok2 {
			return nil
		}
		return capacityFrom(total, free)
	}
	out.RAM = mem("MemTotal:", "MemAvailable:")
	out.Swap = mem("SwapTotal:", "SwapFree:")
	disk, e := localCommand(ctx, "df", "-B1", "--output=size,used", store.Dir())
	if v := strings.Fields(disk); e == nil && len(v) == 4 {
		total, e1 := strconv.ParseInt(v[2], 10, 64)
		used, e2 := strconv.ParseInt(v[3], 10, 64)
		if e1 == nil && e2 == nil && used >= 0 && used <= total {
			out.Disk = &capacity{used, total}
		}
	}
	raw, _ = os.ReadFile("/proc/uptime")
	var up float64
	fmt.Sscanf(string(raw), "%f", &up)
	out.Uptime = int64(up)
	return out
}

func gatewayPeers(ctx context.Context, now time.Time) ([]peer, bool) {
	// One netlink snapshot covers every configured listener.
	data, err := localCommand(ctx, "ss", "-Hnt", "state", "established")
	if err != nil {
		return nil, false
	}
	return parsePeers(data, now), true
}

func parsePeers(data string, now time.Time) []peer {
	counts := map[string]peer{}
	ports := map[string]string{}
	for _, g := range gateways() {
		ports[g.port] = g.unit
	}
	for _, line := range strings.Split(data, "\n") {
		v := strings.Fields(line)
		if len(v) < 4 {
			continue
		}
		_, port, e := net.SplitHostPort(v[2])
		gw, ok := ports[port]
		if e != nil || !ok {
			continue
		}
		remote, _, e := net.SplitHostPort(v[3])
		if e != nil {
			continue
		}
		ip, e := netip.ParseAddr(remote)
		if e != nil {
			continue
		}
		ipText := ip.Unmap().String()
		key := gw + " " + ipText
		p := counts[key]
		p.Gateway = gw
		p.IP = ipText
		p.Sockets++
		p.LastSeen = now
		counts[key] = p
	}
	out := make([]peer, 0, len(counts))
	for _, p := range counts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Gateway != out[j].Gateway {
			return out[i].Gateway < out[j].Gateway
		}
		return out[i].IP < out[j].IP
	})
	return out
}

// Count attributed sessions with recent completed usage, including traffic behind the same IP.
func recentUsageSessions(now time.Time) *int {
	entries, err := os.ReadDir(usage.Dir())
	if err != nil && !os.IsNotExist(err) {
		return nil
	}
	active := make(map[string]bool)
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".ndjson")
		if !ok || !usage.IsValidSessionID(id) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil
		}
		if info.Size() > 0 && info.ModTime().After(now.Add(-15*time.Minute)) {
			active[strings.ToLower(id)] = true
		}
	}
	count := len(active)
	return &count
}

func accountHealth() ([]accountCheck, error) {
	creds, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]accountCheck, 0, len(creds))
	for _, c := range creds {
		state := "accepted"
		provider := c.ProviderName()
		if provider != "claude" && provider != "grok" && provider != "codex" {
			state = "unverified"
		} else if c.IsExpired() {
			state = "access token expired"
		} else {
			q := fetchQuota(c)
			switch {
			case strings.Contains(q.Error, "HTTP 401") || strings.Contains(q.Error, "token_revoked"):
				state = "authorization rejected"
			case strings.Contains(q.Error, "HTTP 429"):
				state = "rate limited"
			case !q.Fetched || q.Error != "":
				state = "unverified"
			}
		}
		out = append(out, accountCheck{c.ID, c.Name, c.ProviderName(), state, time.Now().UTC()})
	}
	return out, nil
}
