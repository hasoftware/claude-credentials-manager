package devices

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayIsolationAndCancellation(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	g, err := Gateway("Main", "https://gateway.example", "shared-A")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Gateway("Other", "https://other.example", "shared-B")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := Mint(g, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:5555"
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.Header.Set("CF-IPCountry", "US")
	if _, e := Authenticate(other, token, false, r); e == nil {
		t.Fatal("cross-gateway ticket")
	}
	id, e := Authenticate(g, token, false, r)
	if e != nil {
		t.Fatal(e)
	}
	row, _ := read(id)
	if row.IP != "10.1.2.3" || row.Location != "Private network" {
		t.Fatal("trusted spoofed forwarder")
	}
	ctx, stop, e := Watch(context.Background(), id, g, false)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	list, _ := List()
	found := false
	for _, v := range list {
		if v.ID == id && v.Active == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("live request missing")
	}
	legacy, e := Authenticate(g, "shared-A", true, r)
	if e != nil {
		t.Fatal(e)
	}
	if e := Revoke(legacy); e != nil {
		t.Fatal(e)
	}
	if _, e = Authenticate(g, "shared-A", true, r); e == nil {
		t.Fatal("blocked IP accepted")
	}
	r.RemoteAddr = "10.1.2.4:4444"
	if _, e = Authenticate(g, "shared-A", true, r); e != nil {
		t.Fatal(e)
	}
	if e = Revoke(g); e != nil {
		t.Fatal(e)
	}
	if _, e = Authenticate(g, "shared-A", true, r); e == nil {
		t.Fatal("disabled shared ticket accepted")
	}
	if _, e = Authenticate(g, token, false, r); e != nil {
		t.Fatal("independent ticket affected", e)
	}
	if e = Revoke(id); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stream not cancelled")
	}
	if _, e = Authenticate(g, token, false, r); e == nil {
		t.Fatal("revoked ticket accepted on new IP")
	}
}
func TestRememberedBrowserCannotRecreateRevokedSession(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	t.Setenv("CCM_DEVICE_CF_LOOPBACK", "1")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.4")
	r.Header.Set("CF-IPCountry", "VN")
	id := ID("web", "issuer", "client", "test-sid")
	if err := Web(id, "admin", r, 123, true); err != nil {
		t.Fatal(err)
	}
	row, e := read(id)
	if e != nil || row.Login != 123 || row.IP != "203.0.113.4" || row.Location != "VN" {
		t.Fatal(row, e)
	}
	if e = Revoke(id); e != nil {
		t.Fatal(e)
	}
	if Web(id, "admin", r, 123, true) == nil || Web(id, "", r, 0, false) == nil {
		t.Fatal("revoked browser restored from SSO")
	}
}
