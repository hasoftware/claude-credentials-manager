package share

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/share/alias"
)

type countBearer struct{ calls int }

func (b *countBearer) Fresh() (string, error) { b.calls++; return "codex-only", nil }

func TestAdditionalGrokRoutePreservesProviders(t *testing.T) {
	aliases, err := alias.Parse([]string{"claude-*=gpt-6.2-sol"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model, ticket, want string
		status              int
	}{
		{"grok-4.7", "gateway", "grok", 200}, {"uthink-oai", "gateway", "codex", 200},
		{"claude-ccm-gpt-6.2-sol", "gateway", "codex", 200},
		{"claude-opus-5-5", "gateway", "", 400}, {"claude-sonnet-5-5", "gateway", "", 400},
		{"claude-haiku-5-5", "gateway", "", 400}, {"grok-4.7", "invalid", "", 401},
	} {
		t.Run(tc.model+"-"+tc.ticket, func(t *testing.T) {
			var route string
			bearer := &countBearer{}
			grok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { route = "grok" })
			codex := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				route = "codex"
				if r.Header.Get("Authorization") != "Bearer codex-only" {
					t.Error("missing Codex bearer")
				}
			})
			p := &Proxy{aliasMap: aliases, extraGrok: grok, bearerSrc: bearer, sharedSecret: "gateway"}
			req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"`+tc.model+`"}`))
			req.Header.Set("Authorization", "Bearer "+tc.ticket)
			w := httptest.NewRecorder()
			p.pipelineHandler(codex).ServeHTTP(w, req)
			if route != tc.want || w.Code != tc.status || (bearer.calls > 0) != (tc.want == "codex") {
				t.Fatalf("route=%s status=%d Codex bearer lookups=%d", route, w.Code, bearer.calls)
			}
		})
	}
}
