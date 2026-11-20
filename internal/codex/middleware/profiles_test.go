package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/codex/identity"
	codexmw "github.com/hasoftware/claude-credentials-manager/v2/internal/codex/middleware"
)

func TestCodexProfileOnWire(t *testing.T) {
	for _, tc := range []struct {
		alias, model, effort, speed, tier string
	}{
		{"uthink", "gpt-6.2-sol", "low", "", ""},
		{"udeep-oai", "gpt-6.2-astra", "max", "fast", "priority"},
		{"ulens-oai", "gpt-6.1-terra", "xhigh", "standard", ""},
		{"udash", "gpt-6-luna", "medium", "fast", "priority"},
		{"uthink-oai", "gpt-6.2-sol", "high", "standard", ""},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body struct {
					Model     string
					Tier      string `json:"service_tier"`
					Reasoning struct{ Effort string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != tc.model || body.Tier != tc.tier || body.Reasoning.Effort != tc.effort {
					t.Errorf("received model=%q tier=%q effort=%q", body.Model, body.Tier, body.Reasoning.Effort)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			cred := minimalCred("test")
			term := codexmw.NewTerminal(codexmw.TerminalOpts{Cred: cred, Transport: newTransport(t), Bundle: identity.New(cred), UpstreamURL: upstream.URL})
			body := `{"model":"` + tc.alias + `","output_config":{"effort":"` + tc.effort + `"},"speed":"` + tc.speed + `","messages":[{"role":"user","content":"hi"}]}`
			req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			rr := httptest.NewRecorder()
			withAlias(t, "", term, req, rr)
			if calls.Load() != 1 {
				t.Fatalf("expected one upstream request, got %d", calls.Load())
			}
			if rr.Code >= 400 {
				t.Fatalf("gateway HTTP %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}
