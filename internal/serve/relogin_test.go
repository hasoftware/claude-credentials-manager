package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
)

func TestWebReloginKeepsGatewayCredential(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	id := uuid.NewString()
	old := &store.Credential{ID: id, Name: "personal", Provider: "codex", CreatedAt: "before", Tokens: &store.CodexTokens{AccessToken: "old", AccountID: "account"}}
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: ServerConfig{Manager: NewManager(nil, nil)}}
	begin := httptest.NewRequest("POST", "/api/credentials/"+id+"/relogin/start", nil)
	begin.Header.Set("Content-Type", "application/json")
	begin.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.reloginStart(w, begin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var b APILoginCodexStartResponse
	json.Unmarshal(w.Body.Bytes(), &b)
	c := w.Result().Cookies()[0]
	a := h.relogins.byID[id]
	if time.Until(a.deadline) <= 0 {
		t.Fatal("expired")
	}
	a.exchange = func(string) (*store.Credential, error) {
		return &store.Credential{ID: uuid.NewString(), Name: "upstream", Provider: "codex", Tokens: &store.CodexTokens{AccessToken: "new", AccountID: "account"}}, nil
	}
	h.relogins.byID[id] = a
	finish := func(cookie *http.Cookie) int {
		body, _ := json.Marshal(APILoginCodexFinishRequest{State: b.State, RedirectURL: a.callback + "?code=test&state=" + b.State})
		r := httptest.NewRequest("POST", "/api/credentials/"+id+"/relogin/finish", strings.NewReader(string(body)))
		r.SetPathValue("id", id)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.reloginFinish(w, r)
		return w.Code
	}
	if finish(nil) != 403 || finish(c) != 200 || finish(c) == 200 {
		t.Fatal("browser binding or one-time finish failed")
	}
	disk, e := store.Load(id)
	all, le := store.List()
	if e != nil || le != nil || len(all) != 1 || disk.CreatedAt != "before" || disk.Name != "personal" || disk.AccessToken() != "new" {
		t.Fatal("did not update original credential")
	}
}

func TestAddGrokCredential(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	h := &handler{cfg: ServerConfig{Manager: NewManager(nil, nil)}}
	base := "/api/login/grok/"
	req := httptest.NewRequest("POST", base+"start", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.grokAddStart(w, req)
	var start APILoginCodexStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil || w.Code != 200 || start.State == "" {
		t.Fatal("start failed", err, w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Path != base || !cookie.HttpOnly {
		t.Fatal("wrong cookie scope")
	}
	key := "add:grok:" + start.State
	a := h.relogins.byID[key]
	id := uuid.NewString()
	a.exchange = func(string) (*store.Credential, error) {
		return &store.Credential{ID: id, Provider: "grok", Name: "Example", GrokTokens: &store.GrokTokens{AccessToken: "private-test-token"}}, nil
	}
	h.relogins.byID[key] = a
	finish := func(bound bool) int {
		payload, _ := json.Marshal(APILoginCodexFinishRequest{State: start.State, RedirectURL: a.callback + "?code=test&state=" + start.State})
		r := httptest.NewRequest("POST", base+"finish", strings.NewReader(string(payload)))
		r.Header.Set("Content-Type", "application/json")
		if bound {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		h.grokAddFinish(out, r)
		if strings.Contains(out.Body.String(), "private-test-token") {
			t.Fatal("secret in response")
		}
		return out.Code
	}
	if finish(false) != 403 || finish(true) != 201 || finish(true) != 400 {
		t.Fatal("unexpected callback status")
	}
	c, err := store.Load(id)
	if err != nil || c.ProviderName() != "grok" || c.AccessToken() != "private-test-token" {
		t.Fatal("missing credential")
	}
}
