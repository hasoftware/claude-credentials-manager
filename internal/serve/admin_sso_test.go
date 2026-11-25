package serve

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

func TestAdminSSO(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://id.example"
	var claims map[string]any
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("code_verifier") == "" {
			t.Error("missing PKCE verifier")
		}
		raw, _ := json.Marshal(claims)
		sig, _ := signer.Sign(raw)
		jwt, _ := sig.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token_type": "Bearer", "access_token": "test", "id_token": jwt})
	}))
	defer exchange.Close()
	block, _ := aes.NewCipher(make([]byte, 32))
	seal, _ := cipher.NewGCM(block)
	s := &AdminSSO{origin: "https://admin.example", emails: allowList("operator@example.com", true), seal: seal,
		oauth:    oauth2.Config{ClientID: "admin", ClientSecret: "test", Endpoint: oauth2.Endpoint{AuthURL: issuer + "/authorize", TokenURL: exchange.URL}},
		verifier: oidc.NewVerifier(issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "admin"})}
	handler := s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	request := func(method, path string, c *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, s.origin+path, nil).WithContext(context.Background())
		if c != nil {
			r.AddCookie(c)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/credentials?token=test", nil, ""); w.Code != 401 {
		t.Fatalf("public API: %d", w.Code)
	}
	for _, tc := range []struct {
		email    string
		verified bool
		nonceOK  bool
		want     int
	}{{"operator@example.com", true, true, 303}, {"stranger@example.com", true, true, 403}, {"operator@example.com", false, true, 403}, {"operator@example.com", true, false, 403}} {
		start := request("GET", "/", nil, "")
		u, _ := url.Parse(start.Header().Get("Location"))
		q := u.Query()
		nonce := q.Get("nonce")
		if q.Get("code_challenge_method") != "S256" || nonce == "" {
			t.Fatal("missing PKCE or nonce")
		}
		if !tc.nonceOK {
			nonce = "wrong"
		}
		claims = map[string]any{"iss": issuer, "sub": "user", "aud": "admin", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "email": tc.email, "email_verified": tc.verified}
		complete := request("GET", "/auth/callback?code=one&state="+q.Get("state"), start.Result().Cookies()[0], "")
		if complete.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.email, complete.Code, complete.Body)
		}
		if tc.want == 303 {
			var cookie *http.Cookie
			for _, c := range complete.Result().Cookies() {
				if c.Name == sessionName {
					cookie = c
				}
			}
			if cookie == nil || !cookie.Secure || !cookie.HttpOnly || request("GET", "/api/credentials", cookie, "").Code != 204 {
				t.Fatal("missing authenticated session")
			}
			if request("POST", "/api/credentials/example", cookie, "https://evil.example").Code != 403 {
				t.Fatal("cross-site write")
			}
			cookie.Value = "invalid" + strings.TrimSpace(cookie.Value)
			if request("GET", "/api/credentials", cookie, "").Code != 401 {
				t.Fatal("tampered cookie accepted")
			}
		}
	}
}
