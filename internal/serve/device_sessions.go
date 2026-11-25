package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/devices"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/share"
)

func (s *AdminSSO) deviceAPI(w http.ResponseWriter, r *http.Request, current string) {
	if !s.manageDevices || !devices.Enabled() {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/system/devices":
		rows, e := devices.List()
		if e != nil {
			writeJSON(w, 503, map[string]string{"error": "Device registry unavailable"})
			return
		}
		writeJSON(w, 200, map[string]any{"devices": rows, "current": current})
	case r.Method == "POST" && r.URL.Path == "/api/system/devices/tickets":
		var body struct{ Gateway, Name string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body) != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid device ticket request"})
			return
		}
		token, g, e := devices.Mint(body.Gateway, body.Name)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "Choose a registered gateway and enter a device name (up to 80 bytes)."})
			return
		}
		u, _ := url.Parse(g.Endpoint)
		writeJSON(w, 201, map[string]string{"ticket": share.Ticket{Scheme: u.Scheme, Host: u.Host, Token: token}.Encode()})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/revoke"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/system/devices/"), "/revoke")
		if id == current {
			writeJSON(w, 409, map[string]string{"error": "Use Sign out for this browser."})
			return
		}
		e := devices.Revoke(id)
		if errors.Is(e, os.ErrNotExist) || errors.Is(e, devices.ErrDenied) {
			writeJSON(w, 404, map[string]string{"error": "Device not found"})
			return
		}
		if e != nil {
			writeJSON(w, 503, map[string]string{"error": "Could not revoke access"})
			return
		}
		writeJSON(w, 200, map[string]bool{"revoked": true})
	default:
		http.NotFound(w, r)
	}
}
