package serve

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

// WithReloginPortal serves the provider reconnection UI on its own OIDC origin.
// Only explicitly listed routes are available on that host.
func WithReloginPortal(ctx context.Context, origin string, admin, inner http.Handler) (http.Handler, error) {
	portal, err := newAdminSSO(ctx, strings.TrimRight(origin, "/"))
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(portal.origin)
	if portal.origin == strings.TrimRight(os.Getenv("CCM_ADMIN_ORIGIN"), "/") {
		return nil, fmt.Errorf("relogin portal needs its own origin")
	}
	routes := http.NewServeMux()
	routes.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		creds, err := storeListFn()
		if err != nil {
			http.Error(w, "Could not read provider accounts", 500)
			return
		}
		accounts := []portalAccount{}
		for _, c := range creds {
			p := c.ProviderName()
			if p == "codex" || p == "grok" {
				accounts = append(accounts, portalAccount{c.ID, c.Name, p, c.Status()})
			}
		}
		target := r.URL.Query().Get("account")
		if target != "" {
			filtered := []portalAccount{}
			for _, a := range accounts {
				if strings.EqualFold(a.Provider, target) || strings.EqualFold(a.Name, target) || a.ID == target || (len(target) >= 4 && strings.HasPrefix(a.ID, target)) {
					filtered = append(filtered, a)
				}
			}
			accounts = filtered
		}
		sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		portalPage.ExecuteTemplate(w, "layout", struct {
			Title    string
			Accounts []portalAccount
			Selected bool
		}{"Provider access", accounts, target != ""})
	})
	for _, path := range []string{"/static/app.js", "/static/design.css", "/static/fonts/inter-latin-wght-normal.woff2", "/static/fonts/inter-vietnamese-wght-normal.woff2", "/static/style.css"} {
		routes.Handle("GET "+path, inner)
	}
	for _, step := range []string{"start", "finish"} {
		routes.Handle("POST /api/credentials/{id}/relogin/"+step, inner)
	}
	protected := portal.Protect(routes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Host, u.Host) {
			protected.ServeHTTP(w, r)
			return
		}
		admin.ServeHTTP(w, r)
	}), nil
}

type portalAccount struct{ ID, Name, Provider, Status string }

var portalPage = template.Must(template.ParseFS(templatesFS, "templates/layout.html", "templates/portal.html"))
