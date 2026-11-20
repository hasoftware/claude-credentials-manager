package share

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/codex/models"
	grokmw "github.com/hasoftware/claude-credentials-manager/v2/internal/grok/middleware"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/share/alias"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/share/middleware"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/store"
)

func extraGrokHandler(cred *store.Credential, aliases *alias.Map) (http.Handler, error) {
	if target, _ := aliases.Lookup(grokmw.DefaultModel); target != grokmw.DefaultModel {
		return nil, fmt.Errorf("alias for %s conflicts with the Grok profile", grokmw.DefaultModel)
	}
	handlers, err := grokHandlersFn(cred)
	if err != nil {
		return nil, err
	}
	bearer, err := newCredState(cred)
	if err != nil {
		return nil, err
	}
	return grokmw.NewTerminal(grokmw.TerminalOpts{
		Transport: handlers.Transport, UpstreamURL: handlers.UpstreamURL,
		CredentialID: cred.ID, BearerSrc: bearer,
	}), nil
}

// Dispatch after checking the share ticket, before resolving upstream credentials.
func dispatchGrokProfile(codex, grok http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := middleware.OriginalModel(r.Context())
		if model == grokmw.DefaultModel {
			grok.ServeHTTP(w, r)
			return
		}
		_, profile := models.ByAlias(model)
		// Preserve explicitly configured legacy CCM aliases. Native Claude
		// models must never fall through a wildcard alias to another provider.
		customAlias := middleware.AliasMatched(r.Context()) &&
			(!strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "claude-ccm-"))
		switch model {
		case "opus", "sonnet", "haiku", "fable", "default", "opusplan":
			customAlias = false
		}
		if profile || customAlias {
			codex.ServeHTTP(w, r)
			return
		}
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Model %q is not served by this Codex/Grok ticket. For native Claude models, use your Claude-backed session.", model))
	})
}
