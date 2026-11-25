package serve

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// The issuer authenticates the operator. Only configured subjects or verified
// emails are allowed. Gateway credentials remain in the service user's store.
type AdminSSO struct {
	oauth            oauth2.Config
	verifier         *oidc.IDTokenVerifier
	origin           string
	emails, subjects map[string]bool
	logoutEndpoint   string
	signouts         *adminSignouts
	seal             cipher.AEAD
}
type loginCookie struct {
	State, Nonce, Verifier string
	ReturnPath             string `json:",omitempty"`
	Account                string `json:",omitempty"`
	SessionID              string `json:",omitempty"`
	IssuedAt               int64  `json:",omitempty"`
	Subject                string
	Expiry                 int64
}

const sessionName = "__Host-ccm_admin"
const flowName = "__Host-ccm_oidc"

const adminSessionLifetime = 8 * time.Hour

// Both web origins issue short-lived cookies for the same OIDC client. Track
// sign-outs for their maximum lifetime so an already-open tab cannot reuse one.
// A restart also discards the keys that could open those cookies.
type adminSignouts struct {
	mu sync.RWMutex
	at map[string]time.Time
}

func (s *adminSignouts) revoke(id string) {
	if id == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, at := range s.at {
		if !at.Add(adminSessionLifetime).After(now) {
			delete(s.at, key)
		}
	}
	if s.at == nil {
		s.at = make(map[string]time.Time)
	}
	s.at[id] = now
}

func (s *adminSignouts) revoked(c loginCookie) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	at, ok := s.at[c.SessionID]
	return ok && c.IssuedAt <= at.UnixNano()
}

func allowList(raw string, fold bool) map[string]bool {
	m := make(map[string]bool)
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if fold {
			s = strings.ToLower(s)
		}
		if s != "" {
			m[s] = true
		}
	}
	return m
}
func NewAdminSSO(ctx context.Context) (*AdminSSO, error) {
	return newAdminSSO(ctx, strings.TrimRight(os.Getenv("CCM_ADMIN_ORIGIN"), "/"))
}
func newAdminSSO(ctx context.Context, origin string) (*AdminSSO, error) {
	issuer := os.Getenv("CCM_OIDC_ISSUER")
	client := os.Getenv("CCM_OIDC_CLIENT_ID")
	secret := os.Getenv("CCM_OIDC_CLIENT_SECRET")
	emails := allowList(os.Getenv("CCM_ADMIN_EMAILS"), true)
	subjects := allowList(os.Getenv("CCM_ADMIN_SUBJECTS"), false)
	if client == "" || secret == "" || len(emails)+len(subjects) == 0 {
		return nil, errors.New("SSO requires a client ID, client secret and an explicit admin email or subject")
	}
	for _, raw := range []string{issuer, origin} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("SSO issuer and public origin must use HTTPS")
		}
	}
	u, _ := url.Parse(origin)
	if u.Path != "" {
		return nil, errors.New("CCM_ADMIN_ORIGIN must be an HTTPS origin without a path")
	}
	clientHTTP := &http.Client{Timeout: 12 * time.Second}
	// Provider keeps the discovery client's transport for later JWKS fetches.
	ctx = oidc.ClientContext(ctx, clientHTTP)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("SSO discovery: %w", err)
	}
	var discovery struct {
		LogoutEndpoint string `json:"end_session_endpoint"`
	}
	if err := provider.Claims(&discovery); err != nil {
		return nil, err
	}
	if discovery.LogoutEndpoint != "" {
		u, err := url.Parse(discovery.LogoutEndpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, errors.New("SSO logout endpoint must use HTTPS")
		}
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	seal, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AdminSSO{oauth: oauth2.Config{ClientID: client, ClientSecret: secret, Endpoint: provider.Endpoint(), RedirectURL: origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "email"}}, verifier: provider.Verifier(&oidc.Config{ClientID: client}), origin: origin, emails: emails, subjects: subjects, seal: seal, logoutEndpoint: discovery.LogoutEndpoint, signouts: &adminSignouts{}}, nil
}
func (s *AdminSSO) set(w http.ResponseWriter, name string, data loginCookie) {
	raw, _ := json.Marshal(data)
	iv := make([]byte, s.seal.NonceSize())
	_, _ = rand.Read(iv)
	value := base64.RawURLEncoding.EncodeToString(s.seal.Seal(iv, iv, raw, []byte(name)))
	cookie := &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	// A session-enabled issuer owns the browser's persistence choice. The
	// expiry inside the sealed app cookie is always enforced on the server.
	if name != sessionName || s.logoutEndpoint == "" {
		cookie.Expires = time.Unix(data.Expiry, 0)
	}
	http.SetCookie(w, cookie)
}
func clearAdminCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}
func (s *AdminSSO) read(r *http.Request, name string) (loginCookie, bool) {
	var data loginCookie
	c, err := r.Cookie(name)
	if err != nil {
		return data, false
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	n := s.seal.NonceSize()
	if err != nil || len(b) < n {
		return data, false
	}
	raw, err := s.seal.Open(nil, b[:n], b[n:], []byte(name))
	if err != nil {
		return data, false
	}
	err = json.Unmarshal(raw, &data)
	return data, err == nil && time.Now().Unix() < data.Expiry
}
func (s *AdminSSO) start(w http.ResponseWriter, r *http.Request) {
	a := loginCookie{State: oauth2.GenerateVerifier(), Nonce: oauth2.GenerateVerifier(), Verifier: oauth2.GenerateVerifier(), Expiry: time.Now().Add(10 * time.Minute).Unix()}
	if r.URL.Path == "/" && len(r.URL.Query().Get("account")) <= 256 {
		a.Account = r.URL.Query().Get("account")
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/accounts", "/tutorial", "/system":
		a.ReturnPath = strings.TrimSuffix(r.URL.Path, "/")
	}
	s.set(w, flowName, a)
	http.Redirect(w, r, s.oauth.AuthCodeURL(a.State, oidc.Nonce(a.Nonce), oauth2.S256ChallengeOption(a.Verifier)), http.StatusFound)
}
func (s *AdminSSO) callback(w http.ResponseWriter, r *http.Request) {
	a, ok := s.read(r, flowName)
	clearAdminCookie(w, flowName)
	if !ok || a.State == "" || r.URL.Query().Get("state") != a.State || r.URL.Query().Get("code") == "" {
		http.Error(w, "Login expired or cancelled. Open the dashboard to try again.", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	t, err := s.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(a.Verifier))
	if err != nil {
		http.Error(w, "SSO token exchange failed", 403)
		return
	}
	raw, _ := t.Extra("id_token").(string)
	id, err := s.verifier.Verify(ctx, raw)
	if err != nil || id.Nonce != a.Nonce || id.Subject == "" {
		http.Error(w, "Invalid SSO identity", 403)
		return
	}
	var c struct {
		SessionID string `json:"sid"`
		Email     string
		Verified  bool `json:"email_verified"`
	}
	if err = id.Claims(&c); err != nil || !(s.subjects[id.Subject] || (c.Verified && s.emails[strings.ToLower(c.Email)])) {
		http.Error(w, "This account has no admin access", 403)
		return
	}
	now := time.Now()
	expiry := now.Add(adminSessionLifetime)
	if id.Expiry.Before(expiry) {
		expiry = id.Expiry
	}
	if c.SessionID == "" {
		c.SessionID = oauth2.GenerateVerifier()
	}
	s.set(w, sessionName, loginCookie{Subject: id.Subject, SessionID: c.SessionID, IssuedAt: now.UnixNano(), Expiry: expiry.Unix()})
	target := "/"
	if a.ReturnPath == "/accounts" || a.ReturnPath == "/tutorial" || a.ReturnPath == "/system" {
		target = a.ReturnPath
	}
	if a.Account != "" {
		target += "?" + url.Values{"account": {a.Account}}.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (s *AdminSSO) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != s.origin {
			http.Error(w, "Invalid admin origin", 403)
			return
		}
		switch r.URL.Path {
		case "/auth/callback":
			if r.Method != "GET" {
				http.Error(w, "Use GET", 405)
				return
			}
			s.callback(w, r)
			return
		case "/auth/logout":
			if r.Method != "POST" {
				http.Error(w, "Use POST", 405)
				return
			}
			if c, ok := s.read(r, sessionName); ok {
				s.signouts.revoke(c.SessionID)
			}
			clearAdminCookie(w, sessionName)
			target := s.logoutEndpoint
			if target == "" {
				target = "/signed-out"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		case "/static/appearance.js", "/static/style.css", "/static/design.css", "/static/fonts/inter-latin-wght-normal.woff2", "/static/fonts/inter-vietnamese-wght-normal.woff2":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "Use GET", http.StatusMethodNotAllowed)
				return
			}
			next.ServeHTTP(w, r)
			return
		case "/signed-out":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			page, _ := templatesFS.ReadFile("templates/signedout.html")
			_, _ = w.Write(page)
			return
		}
		if a, ok := s.read(r, sessionName); !ok || a.Subject == "" || s.signouts.revoked(a) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, 401, map[string]string{"error": "SSO login required"})
				return
			}
			if r.Method != "GET" {
				http.Error(w, "SSO login required", 401)
				return
			}
			s.start(w, r)
			return
		}
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "/", 303)
			return
		}
		next.ServeHTTP(w, r)
	})
}
