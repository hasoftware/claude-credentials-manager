package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
)

type monitorSettings struct {
	Telegram                           bool
	ChatID, BotToken                   string
	AccountID, TunnelID, CloudToken    string
	TrustedConnectors, TrustedNetworks []string
}

func monitorPath(file string) string { return filepath.Join(store.Dir(), "monitor", file) }
func readMonitorFile(name string, v any) error {
	b, e := os.ReadFile(monitorPath(name))
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func saveMonitorFile(name string, v any) error {
	path := monitorPath(name)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".monitor-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(v)
	c := f.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return c
	}
	return os.Rename(f.Name(), path)
}
func settings() (monitorSettings, error) {
	var s monitorSettings
	err := readMonitorFile("settings.json", &s)
	if os.IsNotExist(err) {
		err = nil
	}
	return s, err
}

var configLock sync.Mutex
var botPattern = regexp.MustCompile(`^[0-9]{4,20}:[A-Za-z0-9_-]{20,128}$`)
var chatPattern = regexp.MustCompile(`^(-?[0-9]{1,22}|@[A-Za-z0-9_]{5,32})$`)
var accountPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

func (s monitorSettings) validate() error {
	if s.BotToken != "" && !botPattern.MatchString(s.BotToken) {
		return errors.New("Invalid Telegram bot token")
	}
	if (s.ChatID != "" && !chatPattern.MatchString(s.ChatID)) || (s.Telegram && (s.BotToken == "" || s.ChatID == "")) {
		return errors.New("Enter a Telegram bot token and chat ID before enabling alerts")
	}
	if s.CloudToken != "" || s.TunnelID != "" || s.AccountID != "" {
		if !accountPattern.MatchString(s.AccountID) {
			return errors.New("Enter a 32-character Cloudflare account ID")
		}
		if _, e := uuid.Parse(s.TunnelID); e != nil || len(s.TunnelID) != 36 {
			return errors.New("Enter a valid tunnel UUID")
		}
		if s.CloudToken == "" || strings.ContainsAny(s.CloudToken, " \t\r\n") {
			return errors.New("Enter a Cloudflare read token")
		}
	}
	for _, id := range s.TrustedConnectors {
		if _, e := uuid.Parse(id); e != nil || len(id) != 36 {
			return errors.New("Trusted connectors must be UUIDs")
		}
	}
	for _, cidr := range s.TrustedNetworks {
		if _, e := netip.ParsePrefix(cidr); e != nil {
			return errors.New("Use CIDR notation for trusted gateway networks")
		}
	}
	return nil
}
func (h *handler) monitorConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	configLock.Lock()
	defer configLock.Unlock()
	s, e := settings()
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "Cannot read monitor settings"})
		return
	}
	if r.Method == "POST" {
		if !h.cfg.SSO {
			writeJSON(w, 403, map[string]string{"error": "Sign in with SSO to change monitoring settings"})
			return
		}
		if !reloginRequestOK(w, r) {
			return
		}
		var in struct {
			monitorSettings
			ClearCloud, ClearBot bool
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid settings"})
			return
		}
		if in.BotToken == "" {
			in.BotToken = s.BotToken
		}
		if in.ClearBot {
			in.BotToken = ""
		}
		if in.CloudToken == "" {
			in.CloudToken = s.CloudToken
		}
		if in.ClearCloud {
			in.AccountID = ""
			in.TunnelID = ""
			in.CloudToken = ""
		}
		s = in.monitorSettings
		if e = s.validate(); e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if e = saveMonitorFile("settings.json", s); e != nil {
			writeJSON(w, 500, map[string]string{"error": "Could not save settings"})
			return
		}
	}
	bot, cloud := s.BotToken != "", s.CloudToken != ""
	s.BotToken = ""
	s.CloudToken = ""
	writeJSON(w, 200, struct {
		monitorSettings
		HasBot, HasCloud bool
	}{s, bot, cloud})
}

var integrationClient = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func sendTelegram(ctx context.Context, s monitorSettings, message string) bool {
	b, _ := json.Marshal(map[string]string{"chat_id": s.ChatID, "text": message})
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+s.BotToken+"/sendMessage", bytes.NewReader(b))
	if e != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := integrationClient.Do(req)
	if e != nil {
		return false
	}
	defer resp.Body.Close()
	var out struct{ OK bool }
	return resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out) == nil && out.OK
}
func (h *handler) monitorTest(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.SSO || !reloginRequestOK(w, r) {
		if !h.cfg.SSO {
			http.Error(w, "SSO required", 403)
		}
		return
	}
	s, e := settings()
	if e != nil || s.BotToken == "" || s.ChatID == "" {
		writeJSON(w, 400, map[string]string{"error": "Save a bot token and chat ID first"})
		return
	}
	if !sendTelegram(r.Context(), s, "HASOFT CCM2 · monitoring test") {
		writeJSON(w, 502, map[string]string{"error": "Delivery failed. Check the bot token, chat ID and bot access."})
		return
	}
	writeJSON(w, 200, map[string]bool{"sent": true})
}

type cloudConnector struct {
	ID, Version string
	Origins     []string
	Edges       int
	Known       bool
}
type cloudView struct {
	State      string
	At         time.Time
	Connectors []cloudConnector
}

func cloudInventory(ctx context.Context, s monitorSettings, local string) cloudView {
	out := cloudView{State: "not configured", At: time.Now().UTC()}
	if s.CloudToken == "" {
		return out
	}
	out.State = "unavailable"
	path := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s/connections", s.AccountID, s.TunnelID)
	req, e := http.NewRequestWithContext(ctx, "GET", path, nil)
	if e != nil {
		return out
	}
	req.Header.Set("Authorization", "Bearer "+s.CloudToken)
	resp, e := integrationClient.Do(req)
	if e != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out
	}
	var body struct {
		Success bool
		Info    struct {
			Total int `json:"total_count"`
		} `json:"result_info"`
		Result []struct {
			ID, Version string
			Conns       []struct {
				OriginIP string `json:"origin_ip"`
				Pending  bool   `json:"is_pending_reconnect"`
			}
		}
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body) != nil || !body.Success || body.Result == nil || body.Info.Total > len(body.Result) {
		return out
	}
	known := map[string]bool{strings.ToLower(local): true}
	for _, id := range s.TrustedConnectors {
		known[strings.ToLower(id)] = true
	}
	for _, r := range body.Result {
		c := cloudConnector{ID: r.ID, Version: r.Version, Known: known[strings.ToLower(r.ID)]}
		origins := map[string]bool{}
		for _, link := range r.Conns {
			if link.Pending {
				continue
			}
			c.Edges++
			if link.OriginIP != "" {
				origins[link.OriginIP] = true
			}
		}
		if c.Edges == 0 {
			continue
		}
		for ip := range origins {
			c.Origins = append(c.Origins, ip)
		}
		sort.Strings(c.Origins)
		out.Connectors = append(out.Connectors, c)
	}
	out.State = "reported"
	return out
}
