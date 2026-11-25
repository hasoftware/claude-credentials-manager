package serve

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/usage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type monitorTransport func(*http.Request) (*http.Response, error)

func (fn monitorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func TestCloudInventoryCountsProcessesNotLinks(t *testing.T) {
	prev := integrationClient
	defer func() { integrationClient = prev }()
	integrationClient = &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Fatal("wrong inventory request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":[{"id":"here","version":"1","conns":[{"origin_ip":"192.0.2.1"},{"origin_ip":"192.0.2.1"}]},{"id":"other","conns":[{}]},{"id":"old","conns":[{"is_pending_reconnect":true}]}]}`))}, nil
	})}
	v := cloudInventory(context.Background(), monitorSettings{CloudToken: "fixture"}, "here")
	if v.State != "reported" || len(v.Connectors) != 2 || v.Connectors[0].Edges != 2 || len(v.Connectors[0].Origins) != 1 || !v.Connectors[0].Known || v.Connectors[1].Known {
		t.Fatal(v)
	}
}
func TestGatewayPeerTracking(t *testing.T) {
	t.Setenv("CCM_MONITOR_STAGING", "1")
	now := time.Now()
	peers := parsePeers("0 0 10.0.0.1:17879 [::ffff:192.0.2.8]:5555\n0 0 10.0.0.1:17879 192.0.2.8:4444\n0 0 10.0.0.1:17878 192.0.2.8:3333\n0 0 10.0.0.1:22 192.0.2.8:1234", now)
	if len(peers) != 2 || peers[0].Sockets != 2 || peers[0].IP != "192.0.2.8" {
		t.Fatal(peers)
	}
	r := monitorReport{At: now, Peers: peers, PeersAvailable: true, Incidents: map[string]incident{}}
	cfg := monitorSettings{TrustedNetworks: []string{"10.0.0.0/8"}}
	r.recordPeers(cfg)
	if len(r.Changes) != 2 || !r.Peers[0].Unexpected {
		t.Fatal(r)
	}
	r.At = now.Add(time.Minute)
	r.Peers = nil
	r.recordPeers(cfg)
	if len(r.Changes) != 2 || len(r.Recent) != 2 || r.Recent[0].Sockets != 0 {
		t.Fatal(r)
	}
	r.At = now.Add(25 * time.Hour)
	r.recordPeers(cfg)
	if len(r.Changes) != 4 || len(r.Recent) != 0 {
		t.Fatal(r)
	}
}
func TestMonitorFailureThresholdAndDelivery(t *testing.T) {
	r := monitorReport{Incidents: map[string]incident{}, At: time.Now()}
	s := monitorSettings{Telegram: true, BotToken: "fake"}
	prev := integrationClient
	defer func() { integrationClient = prev }()
	calls := 0
	integrationClient = &http.Client{Transport: monitorTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	for i := 0; i < 4; i++ {
		r.At = r.At.Add(time.Second)
		r.observe("unit", true, "failed", r.At, 3)
		r.deliver(context.Background(), s)
		if i < 2 && calls != 0 {
			t.Fatal("early alert")
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	r.At = r.At.Add(time.Second)
	r.observe("unit", false, "healthy", r.At, 3)
	r.deliver(context.Background(), s)
	if calls != 2 || len(r.Incidents) > 0 {
		t.Fatal(calls, r.Incidents)
	}
}
func TestMonitorSettingsSecretsWriteOnly(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	token := "123456:im-a-fixture-value-of-sufficient-length"
	h := handler{cfg: ServerConfig{SSO: true}}
	for _, payload := range []string{`{"Telegram":true,"ChatID":"123456","BotToken":"` + token + `"}`, `{"Telegram":true,"ChatID":"123456","BotToken":""}`} {
		r := httptest.NewRequest("POST", "/api/system/settings", strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.monitorConfig(w, r)
		var v struct{ HasBot bool }
		json.Unmarshal(w.Body.Bytes(), &v)
		if w.Code != 200 || !v.HasBot || strings.Contains(w.Body.String(), token) {
			t.Fatal("bad settings response", w.Code)
		}
	}
	s, e := settings()
	if e != nil || s.BotToken != token {
		t.Fatal("secret not preserved")
	}
}

func TestRecentUsageSessions(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	now := time.Now()
	if err := usage.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	entries := map[string]time.Time{
		"12ed68cf-2683-4da7-89ad-f2b268806ac1.ndjson": now,
		"5b05127f-7701-441c-ae2e-771b513da964.ndjson": now.Add(-20 * time.Minute),
		"unattributed-2026-11.ndjson":                 now,
	}
	for name, stamp := range entries {
		path := filepath.Join(usage.Dir(), name)
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if count := recentUsageSessions(now); count == nil || *count != 1 {
		t.Fatalf("recent sessions = %v", count)
	}
}

func TestRouteMapPublishesOnlyHostnames(t *testing.T) {
	routes := parseTunnelRoutes(strings.NewReader(`{"config":{"ingress":[
		{"hostname":"CCM.example.com","path":"/docs","service":"http://private:8787"},
		{"hostname":"ccm.example.com.","service":"https://private:443","originRequest":{"httpHostHeader":"secret"}},
		{"hostname":"*.example.com","service":"https://private:443"},
		{"hostname":"ssh.example.com","service":"ssh://private:22"},
		{"hostname":"docs.example.com","path":"/docs","service":"http://private"},
		{"hostname":"user@invalid.test","service":"http://private"},
		{"service":"http_status:404"}]}}`))
	if len(routes) != 4 || routes[0] != (tunnelRoute{"ccm.example.com", true}) || routes[1].Web || routes[2].Web || routes[3].Web {
		t.Fatalf("routes = %+v", routes)
	}
	encoded, err := json.Marshal(routes)
	if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "secret") {
		t.Fatal("origin settings exposed")
	}
	if routes := parseTunnelRoutes(strings.NewReader(`{"config":{"ingress":[]}}`)); routes == nil || len(routes) != 0 {
		t.Fatal("empty inventory must be distinguishable from unavailable")
	}
	for _, invalid := range []string{"{}", "{", strings.Repeat(" ", (2<<20)+1)} {
		if parseTunnelRoutes(strings.NewReader(invalid)) != nil {
			t.Fatal("malformed or oversized inventory must be unavailable")
		}
	}
}
