package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	codexoauth "github.com/hasoftware/claude-credentials-manager/v2/internal/codex/oauth"
	grokoauth "github.com/hasoftware/claude-credentials-manager/v2/internal/grok/oauth"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
)

type reloginAttempt struct {
	state, browser, provider, callback string
	deadline                           time.Time
	exchange                           func(string) (*store.Credential, error)
}

type reloginRegistry struct {
	mu   sync.Mutex
	byID map[string]reloginAttempt
}

// Dashboard mutations are sent by same-origin fetch with JSON, which cross-site
// HTML forms cannot submit. SSO mode also enforces the configured public origin.
func reloginRequestOK(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]) != "application/json" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSON(w, 403, map[string]string{"error": "Use the dashboard to relogin"})
		return false
	}
	return true
}

func (h *handler) reloginStart(w http.ResponseWriter, r *http.Request) {
	if !reloginRequestOK(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "Unknown credential"})
		return
	}
	cred, err := store.Load(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "Unknown credential"})
		return
	}
	a := reloginAttempt{provider: cred.ProviderName(), browser: uuid.NewString(), deadline: time.Now().Add(loginHandshakeTTL)}
	var link string
	switch a.provider {
	case "codex":
		var p *codexoauth.PKCEParams
		link, p, err = codexLoginBeginFn()
		if err == nil {
			a.state = p.State
			a.callback = codexoauth.DefaultRedirectURI
			a.exchange = func(raw string) (*store.Credential, error) { return exchangeCodexLogin(raw, p) }
		}
	case "grok":
		p, e := grokoauth.GeneratePKCE()
		err = e
		if err == nil {
			a.callback = grokoauth.DefaultRedirectURI
			a.state = p.State
			link = grokoauth.BuildAuthorizeURL(p, a.callback)
			a.exchange = func(raw string) (*store.Credential, error) {
				u, e := url.Parse(raw)
				if e != nil {
					return nil, e
				}
				return grokoauth.CompleteCode(u.Query().Get("code"), p.Verifier, a.callback)
			}
		}
	default:
		writeJSON(w, 400, map[string]string{"error": "Web relogin is available for Codex and Grok"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Could not start authorization"})
		return
	}
	h.relogins.mu.Lock()
	if h.relogins.byID == nil {
		h.relogins.byID = make(map[string]reloginAttempt)
	}
	for key, old := range h.relogins.byID {
		if time.Now().After(old.deadline) {
			delete(h.relogins.byID, key)
		}
	}
	h.relogins.byID[id] = a
	h.relogins.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "ccm_relogin", Value: a.browser, Path: "/api/credentials/" + id + "/relogin/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: int(loginHandshakeTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, APILoginCodexStartResponse{Version: APIVersion, State: a.state, AuthorizeURL: link})
}

func (h *handler) reloginFinish(w http.ResponseWriter, r *http.Request) {
	if !reloginRequestOK(w, r) {
		return
	}
	id := r.PathValue("id")
	var body APILoginCodexFinishRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid response"})
		return
	}
	browser, err := r.Cookie("ccm_relogin")
	if err != nil {
		writeJSON(w, 403, map[string]string{"error": "Open relogin again in this browser"})
		return
	}
	h.relogins.mu.Lock()
	a, found := h.relogins.byID[id]
	valid := found && time.Now().Before(a.deadline) && browser.Value == a.browser && body.State == a.state
	if valid {
		delete(h.relogins.byID, id)
	}
	h.relogins.mu.Unlock()
	if !valid {
		writeJSON(w, 400, map[string]string{"error": "Login session expired. Start over."})
		return
	}
	u, err := url.Parse(strings.TrimSpace(body.RedirectURL))
	if err != nil || u.User != nil || u.Fragment != "" || u.Scheme+"://"+u.Host+u.EscapedPath() != a.callback || u.Query().Get("state") != a.state || u.Query().Get("code") == "" {
		writeJSON(w, 400, map[string]string{"error": "Callback URL or state mismatch. Start over."})
		return
	}
	fresh, err := a.exchange(u.String())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "Provider rejected authorization. Start over."})
		return
	}
	err = store.WithCredentialLock(id, func() error {
		old, e := store.Load(id)
		if e != nil {
			return e
		}
		if old.ProviderName() != a.provider || fresh.ProviderName() != a.provider {
			return errors.New("provider changed")
		}
		if a.provider == "codex" && old.Tokens != nil && old.Tokens.AccountID != "" && (fresh.Tokens == nil || old.Tokens.AccountID != fresh.Tokens.AccountID) {
			return errors.New("account mismatch")
		}
		if strings.Contains(old.Name, "@") && !strings.EqualFold(old.Name, fresh.Name) {
			return errors.New("account mismatch")
		}
		fresh.ID, fresh.Name, fresh.CreatedAt = old.ID, old.Name, old.CreatedAt
		return store.Save(fresh)
	})
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "Unable to update the original credential. Check the selected account and retry."})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, APILoginFinishResponse{Version: APIVersion, Credential: toAPICredential(fresh, h.cfg.Manager)})
}
