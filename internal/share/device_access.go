package share

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/devices"
)

func (p *Proxy) withDeviceAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.modeMu.RLock()
		gateway, secret := p.deviceGateway, p.accessToken
		p.modeMu.RUnlock()
		if gateway == "" || r.URL.Path == "/ccm-share/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeAnthropicError(w, 401, "authentication_error", "Gateway ticket required")
			return
		}
		raw = strings.TrimSpace(raw)
		legacy := subtle.ConstantTimeCompare([]byte(raw), []byte(secret)) == 1
		id, e := devices.Authenticate(gateway, raw, legacy, r)
		if errors.Is(e, devices.ErrDenied) {
			writeAnthropicError(w, 401, "authentication_error", "Gateway ticket is invalid or revoked")
			return
		}
		if e != nil {
			writeAnthropicError(w, 503, "api_error", "Device registry unavailable")
			return
		}
		ctx, stop, e := devices.Watch(r.Context(), id, gateway, legacy)
		if e != nil {
			writeAnthropicError(w, 503, "api_error", "Device registry unavailable")
			return
		}
		defer stop()
		req := r.Clone(ctx)
		req.Header.Set("Authorization", "Bearer "+secret)
		next.ServeHTTP(w, req)
	})
}
