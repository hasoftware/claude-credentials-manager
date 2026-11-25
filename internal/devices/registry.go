// Package devices records authenticated web sessions and gateway tickets in the
// service user's private store, shared by the dashboard and gateway processes.
package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
)

var ErrDenied = errors.New("session revoked or unknown")

type Record struct {
	ID       string
	Kind     string
	Name     string
	Gateway  string `json:",omitempty"`
	Endpoint string `json:",omitempty"`
	IP       string `json:",omitempty"`
	Location string `json:",omitempty"`
	Agent    string `json:",omitempty"`
	Created  int64
	Login    int64
	Seen     int64
	Revoked  int64
	Active   int `json:",omitempty"`
}

func ID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func Enabled() bool { return os.Getenv("CCM_DEVICE_SESSIONS") == "1" }
func dir() string   { return filepath.Join(store.Dir(), "devices") }
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 32 && strings.ToLower(id) == id
}
func read(id string) (Record, error) {
	var r Record
	if !validID(id) {
		return r, ErrDenied
	}
	b, e := os.ReadFile(filepath.Join(dir(), id+".json"))
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r)
	return r, e
}
func save(r Record) error {
	if e := os.MkdirAll(dir(), 0700); e != nil {
		return e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir(), ".record-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(dir(), r.ID+".json"))
}

// Records are atomically replaced under the existing cross-process store lock.
// A queued request can never overwrite a concurrent revocation.
func change(id string, create bool, fn func(*Record) error) error {
	if !validID(id) {
		return ErrDenied
	}
	return store.WithCredentialLock("device-access", func() error {
		r, e := read(id)
		if errors.Is(e, os.ErrNotExist) && create {
			r = Record{ID: id, Created: time.Now().Unix()}
		} else if e != nil {
			return e
		}
		if e = fn(&r); e != nil {
			return e
		}
		return save(r)
	})
}
func List() ([]Record, error) {
	entries, e := os.ReadDir(dir())
	out := []Record{}
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validID(id) {
			continue
		}
		r, e := read(id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	active := map[string]int{}
	for _, entry := range entries {
		id, _, ok := strings.Cut(entry.Name(), ".active-")
		if !ok || !validID(id) {
			continue
		}
		info, e := entry.Info()
		if e == nil && time.Since(info.ModTime()) < 10*time.Second {
			active[id]++
		}
	}
	for i := range out {
		out[i].Active = active[out[i].ID]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seen > out[j].Seen })
	return out, nil
}
func Revoke(id string) error {
	return change(id, false, func(r *Record) error {
		if r.Revoked == 0 {
			r.Revoked = time.Now().Unix()
		}
		return nil
	})
}
func Allowed(id string) bool { r, e := read(id); return e == nil && r.Revoked == 0 }
func Gateway(name, endpoint, secret string) (string, error) {
	u, e := url.Parse(endpoint)
	if name == "" || secret == "" || e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("device gateway requires a name, a ticket secret and an HTTP(S) endpoint")
	}
	id := ID("share", name, secret)
	return id, change(id, true, func(r *Record) error {
		r.Kind = "share"
		r.Name = name
		r.Endpoint = u.Scheme + "://" + u.Host
		return nil
	})
}
func Mint(gateway, name string) (string, Record, error) {
	g, e := read(gateway)
	if e != nil {
		return "", g, e
	}
	name = strings.TrimSpace(name)
	if g.Kind != "share" || name == "" || len(name) > 80 {
		return "", g, errors.New("invalid gateway or device name")
	}
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return "", g, e
	}
	token := "ccm2d." + base64.RawURLEncoding.EncodeToString(key[:])
	id := ID("ticket", token)
	e = change(id, true, func(r *Record) error { r.Kind = "ticket"; r.Name = name; r.Gateway = g.ID; return nil })
	return token, g, e
}

type peer struct{ ip, country, agent string }

func identify(r *http.Request, trustEdge bool) peer {
	raw, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, _ := netip.ParseAddr(raw)
	ip = ip.Unmap()
	country := ""
	if trustEdge && ip.IsLoopback() {
		if real, e := netip.ParseAddr(r.Header.Get("CF-Connecting-IP")); e == nil {
			ip = real.Unmap()
			c := r.Header.Get("CF-IPCountry")
			if len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' && c != "XX" {
				country = c
			}
		}
	}
	addr := ""
	if ip.IsValid() {
		addr = ip.String()
	}
	ua := []rune(r.UserAgent())
	if len(ua) > 180 {
		ua = ua[:180]
	}
	return peer{addr, country, string(ua)}
}
func location(p peer) string {
	if p.country != "" {
		return p.country
	}
	a, e := netip.ParseAddr(p.ip)
	switch {
	case e != nil:
		return "Unknown"
	case a.IsLoopback():
		return "Localhost"
	case a.IsPrivate() || a.IsLinkLocalUnicast():
		return "Private network"
	}
	// Optional local country database. No IP is sent to an external service.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	tool := "geoiplookup"
	if a.Is6() {
		tool = "geoiplookup6"
	}
	b, e := exec.CommandContext(ctx, tool, p.ip).Output()
	if e == nil {
		for _, l := range strings.Split(string(b), "\n") {
			prefix, value, ok := strings.Cut(l, ": ")
			if ok && (prefix == "GeoIP Country Edition" || prefix == "GeoIP Country V6 Edition") {
				code, _, ok := strings.Cut(value, ",")
				if ok && len(code) == 2 {
					return code
				}
			}
		}
	}
	return "Unknown"
}
func observe(id, kind, name, gateway string, p peer, login int64, create bool) error {
	now := time.Now().Unix()
	prior, e := read(id)
	if e == nil {
		if prior.Revoked != 0 || prior.Kind != kind || prior.Gateway != gateway {
			return ErrDenied
		}
		if prior.IP == p.ip && prior.Agent == p.agent && now-prior.Seen < 20 && login <= prior.Login {
			return nil
		}
	} else if !errors.Is(e, os.ErrNotExist) || !create {
		return e
	}
	loc := prior.Location
	if loc == "" || prior.IP != p.ip || p.country != "" {
		loc = location(p)
	}
	return change(id, create, func(r *Record) error {
		if r.Revoked != 0 || (r.Kind != "" && (r.Kind != kind || r.Gateway != gateway)) {
			return ErrDenied
		}
		r.Kind = kind
		r.Gateway = gateway
		if name != "" {
			r.Name = name
		}
		if r.Login == 0 && login == 0 {
			r.Login = now
		}
		if login > r.Login {
			r.Login = login
		}
		r.Seen = now
		r.IP = p.ip
		r.Agent = p.agent
		r.Location = loc
		return nil
	})
}
func Web(id, name string, r *http.Request, login int64, create bool) error {
	return observe(id, "web", name, "", identify(r, os.Getenv("CCM_DEVICE_CF_LOOPBACK") == "1"), login, create)
}

// The legacy ticket is authenticated by the caller before an IP row can be
// created. A named device ticket is always scoped to its original gateway.
func Authenticate(gateway, token string, legacy bool, r *http.Request) (id string, e error) {
	p := identify(r, false)
	if legacy {
		if !Allowed(gateway) || p.ip == "" {
			return "", ErrDenied
		}
		id = ID("legacy", gateway, p.ip)
		e = observe(id, "legacy", "Shared ticket", gateway, p, 0, true)
	} else {
		if len(token) != 49 || !strings.HasPrefix(token, "ccm2d.") {
			return "", ErrDenied
		}
		id = ID("ticket", token)
		e = observe(id, "ticket", "", gateway, p, 0, false)
	}
	if errors.Is(e, os.ErrNotExist) {
		e = ErrDenied
	}
	return id, e
}

// A live request has a lease on disk so the separate dashboard process can
// show active streams. Cancellation propagates to the provider request.
func Watch(parent context.Context, id, gateway string, legacy bool) (context.Context, func(), error) {
	f, e := os.CreateTemp(dir(), id+".active-")
	if e != nil {
		return nil, nil, e
	}
	path := f.Name()
	if e = f.Close(); e != nil {
		os.Remove(path)
		return nil, nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer os.Remove(path)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				if !Allowed(id) || (legacy && !Allowed(gateway)) {
					cancel()
					return
				}
				if e := os.Chtimes(path, now, now); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }, nil
}
