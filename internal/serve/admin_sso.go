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
	seal             cipher.AEAD
}
type loginCookie struct {
	State, Nonce, Verifier string
	Account                string `json:",omitempty"`
	Subject                string
	Expiry                 int64
}

const sessionName = "__Host-ccm_admin"
const flowName = "__Host-ccm_oidc"

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
	return &AdminSSO{oauth: oauth2.Config{ClientID: client, ClientSecret: secret, Endpoint: provider.Endpoint(), RedirectURL: origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "email"}}, verifier: provider.Verifier(&oidc.Config{ClientID: client}), origin: origin, emails: emails, subjects: subjects, seal: seal}, nil
}
func (s *AdminSSO) set(w http.ResponseWriter, name string, data loginCookie) {
	raw, _ := json.Marshal(data)
	iv := make([]byte, s.seal.NonceSize())
	_, _ = rand.Read(iv)
	value := base64.RawURLEncoding.EncodeToString(s.seal.Seal(iv, iv, raw, []byte(name)))
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(data.Expiry, 0)})
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
		Email    string
		Verified bool `json:"email_verified"`
	}
	if err = id.Claims(&c); err != nil || !(s.subjects[id.Subject] || (c.Verified && s.emails[strings.ToLower(c.Email)])) {
		http.Error(w, "This account has no admin access", 403)
		return
	}
	expiry := time.Now().Add(8 * time.Hour)
	if id.Expiry.Before(expiry) {
		expiry = id.Expiry
	}
	s.set(w, sessionName, loginCookie{Subject: id.Subject, Expiry: expiry.Unix()})
	target := "/"
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
			clearAdminCookie(w, sessionName)
			http.Redirect(w, r, "/signed-out", 303)
			return
		case "/static/style.css", "/static/design.css":
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
		if a, ok := s.read(r, sessionName); !ok || a.Subject == "" {
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
