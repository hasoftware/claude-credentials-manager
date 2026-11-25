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
