package serve

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

const samplePeriod = 15 * time.Second

type incident struct {
	Count          int
	Last           time.Time
	Open, Notified bool
	Detail         string
}
type change struct {
	At      time.Time
	Message string
}
type monitorReport struct {
	At                time.Time
	Host              hostUsage
	Services          []serviceCheck
	Tunnel            tunnelLocal
	Routes            []tunnelRoute
	Cloud             cloudView
	Peers             []peer
	PeersAvailable    bool
	RecentSessions    *int
	Recent            []peer
	Accounts          []accountCheck
	AccountsAt        time.Time
	AccountsAvailable bool
	Changes           []change
	Incidents         map[string]incident
	LastDelivery      string
}

func (r *monitorReport) event(message string) {
	r.Changes = append(r.Changes, change{r.At, message})
	if len(r.Changes) > 50 {
		r.Changes = r.Changes[len(r.Changes)-50:]
	}
}
func (r *monitorReport) observe(key string, bad bool, detail string, when time.Time, threshold int) {
	i := r.Incidents[key]
	if i.Last.Equal(when) {
		return
	}
	i.Last = when
	i.Detail = detail
	if bad {
		i.Count++
		if i.Count >= threshold && !i.Open {
			i.Open = true
			r.event(key + " · " + detail)
		}
	} else {
		if i.Open {
			r.event(key + " · recovered")
		}
		i.Open = false
		i.Count = 0
	}
	if !i.Open && !i.Notified && i.Count == 0 {
		delete(r.Incidents, key)
		return
	}
	r.Incidents[key] = i
}
func (r *monitorReport) deliver(ctx context.Context, s monitorSettings) {
	if !s.Telegram {
		return
	}
	var keys, lines []string
	for k, i := range r.Incidents {
		if i.Open != i.Notified {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	for _, k := range keys {
		i := r.Incidents[k]
		status := "recovered"
		if i.Open {
			status = i.Detail
		}
		lines = append(lines, k+" · "+status)
	}
	// Send one bounded notification per cycle.
	if len(keys) > 8 {
		keys = keys[:8]
		lines = lines[:8]
	}
	if !sendTelegram(ctx, s, "HASOFT CCM2 · "+r.Host.Host+"\n"+strings.Join(lines, "\n")) {
		r.LastDelivery = "Failed · " + r.At.Format(time.RFC3339)
		return
	}
	r.LastDelivery = "Delivered · " + r.At.Format(time.RFC3339)
	for _, k := range keys {
		i := r.Incidents[k]
		i.Notified = i.Open
		if !i.Open {
			delete(r.Incidents, k)
		} else {
			r.Incidents[k] = i
		}
	}
}
func (r *monitorReport) recordPeers(s monitorSettings) {
	observed := map[string]peer{}
	for _, p := range r.Recent {
		if r.At.Sub(p.LastSeen) < 24*time.Hour {
			p.Sockets = 0
			observed[p.Gateway+" "+p.IP] = p
		}
	}
	ranges := []netip.Prefix{}
	for _, str := range s.TrustedNetworks {
		if n, e := netip.ParsePrefix(str); e == nil {
			ranges = append(ranges, n)
		}
	}
	allowed := func(ip string) bool {
		if len(ranges) == 0 {
			return true
		}
		addr, e := netip.ParseAddr(ip)
		if e != nil {
			return false
		}
		for _, n := range ranges {
			if n.Contains(addr) {
				return true
			}
		}
		return false
	}
	for n, p := range r.Peers {
		p.Unexpected = !allowed(p.IP)
		r.Peers[n] = p
		observed[p.Gateway+" "+p.IP] = p
	}
	r.Recent = nil
	for _, p := range observed {
		p.Unexpected = !allowed(p.IP)
		r.Recent = append(r.Recent, p)
	}
	sort.Slice(r.Recent, func(i, j int) bool {
		if r.Recent[i].LastSeen.Equal(r.Recent[j].LastSeen) {
			return r.Recent[i].Gateway+r.Recent[i].IP < r.Recent[j].Gateway+r.Recent[j].IP
		}
		return r.Recent[i].LastSeen.After(r.Recent[j].LastSeen)
	})
	if len(r.Recent) > 200 {
		r.Recent = r.Recent[:200]
	}
	if r.PeersAvailable {
		bad := map[string]bool{}
		for _, p := range observed {
			if !allowed(p.IP) {
				bad[p.Gateway+" "+p.IP] = true
			}
		}
		r.inventoryAlerts("Client network: ", bad, "source outside trusted networks", r.At)
	}
}

func (r *monitorReport) inventoryAlerts(prefix string, bad map[string]bool, reason string, at time.Time) {
	for id := range bad {
		r.observe(prefix+id, true, reason, at, 1)
	}
	for key := range r.Incidents {
		id, ok := strings.CutPrefix(key, prefix)
		if ok && !bad[id] {
			r.observe(key, false, "cleared", at, 1)
		}
	}
}

func runAccounts(ctx context.Context, out chan<- accountResult) {
	for {
		list, e := accountHealth()
		select {
		case out <- accountResult{list, e == nil, time.Now().UTC()}:
		case <-ctx.Done():
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}

type accountResult struct {
	values []accountCheck
	ok     bool
	at     time.Time
}

func RunSystemMonitor(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("system monitor requires Linux with systemd and iproute2")
	}
	var r monitorReport
	if e := readMonitorFile("status.json", &r); e != nil && !os.IsNotExist(e) {
		return errors.New("cannot read saved monitor state")
	}
	if r.Incidents == nil {
		r.Incidents = map[string]incident{}
	}
	results := make(chan accountResult, 1)
	go runAccounts(ctx, results)
	var cpu cpuSample
	var cloudAt time.Time
	var prior monitorSettings
	ticker := time.NewTicker(samplePeriod)
	defer ticker.Stop()
	for {
		s, err := settings()
		if err != nil {
			return errors.New("cannot read monitor settings")
		}
		r.At = time.Now().UTC()
		r.Host = resources(ctx, &cpu)
		r.Services = services(ctx)
		for j, sc := range r.Services {
			if sc.State == "active" {
				for _, g := range gateways() {
					if g.unit == sc.Name {
						conn, e := net.DialTimeout("tcp", "127.0.0.1:"+g.port, time.Second)
						if e != nil {
							sc.State = "unreachable"
							sc.Detail = "listener failed"
						} else {
							conn.Close()
						}
					}
				}
			}
			r.Services[j] = sc
			r.observe(sc.Name, sc.State != "active", sc.State+" / "+sc.Detail, r.At, 3)
		}
		r.Tunnel = tunnelProbe(ctx)
		r.Routes = localTunnelRoutes(ctx)
		r.observe("Local tunnel readiness", !r.Tunnel.Ready, "no ready connection confirmed", r.At, 3)
		if time.Since(cloudAt) >= time.Minute || s.CloudToken != prior.CloudToken || s.AccountID != prior.AccountID || s.TunnelID != prior.TunnelID || strings.Join(s.TrustedConnectors, ",") != strings.Join(prior.TrustedConnectors, ",") {
			r.Cloud = cloudInventory(ctx, s, r.Tunnel.ID)
			cloudAt = r.Cloud.At
			r.RecentSessions = recentUsageSessions(r.At)
			r.observe("Cloudflare inventory API", r.Cloud.State == "unavailable", "inventory unavailable", cloudAt, 3)
			if r.Cloud.State == "reported" {
				unknown := map[string]bool{}
				for _, c := range r.Cloud.Connectors {
					if !c.Known {
						unknown[c.ID] = true
					}
				}
				r.observe("Tunnel inventory", len(r.Cloud.Connectors) == 0, "no active cloudflared connector", cloudAt, 2)
				r.inventoryAlerts("Connector: ", unknown, "active connector outside trusted inventory", cloudAt)
			} else if r.Cloud.State == "not configured" {
				r.inventoryAlerts("Connector: ", nil, "inventory disabled", cloudAt)
				r.observe("Tunnel inventory", false, "inventory disabled", cloudAt, 1)
			}
		}
		prior = s
		r.Peers, r.PeersAvailable = gatewayPeers(ctx, r.At)
		r.recordPeers(s)
		select {
		case a := <-results:
			r.AccountsAt = a.at
			r.AccountsAvailable = a.ok
			if a.ok {
				r.Accounts = a.values
				seen := map[string]bool{}
				for _, v := range r.Accounts {
					k := "Account " + v.Provider + " " + v.ID
					seen[k] = true
					switch v.State {
					case "authorization rejected":
						r.observe(k, true, "provider rejected credential", v.At, 1)
					case "access token expired":
						r.observe(k, true, "access token expired; renewal not confirmed", v.At, 3)
					case "accepted":
						r.observe(k, false, "provider accepted credential", v.At, 1)
					}
				}
				for k := range r.Incidents {
					if strings.HasPrefix(k, "Account ") && !seen[k] {
						delete(r.Incidents, k)
					}
				}
			}
		default:
		}
		r.deliver(ctx, s)
		if err := saveMonitorFile("status.json", r); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (h *handler) monitorStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	f, e := os.Open(monitorPath("status.json"))
	if os.IsNotExist(e) {
		writeJSON(w, 200, map[string]bool{"waiting": true})
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "Status unavailable"})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, f)
}
