package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWebReloginPreservesAccountHint(t *testing.T) {
	t.Setenv("CCM_RELOGIN_ORIGIN", "https://sso.example.com")
	old := tryOpenBrowserFn
	var opened string
	tryOpenBrowserFn = func(u string) { opened = u }
	t.Cleanup(func() { tryOpenBrowserFn = old; reloginCmd.Flags().Set("web", "false"); reloginCmd.SetOut(nil) })
	reloginCmd.Flags().Set("web", "true")
	var buf bytes.Buffer
	reloginCmd.SetOut(&buf)
	if err := runRelogin(reloginCmd, []string{"work + home"}); err != nil {
		t.Fatal(err)
	}
	if opened != "https://sso.example.com/?account=work+%2B+home" || !strings.Contains(buf.String(), opened) {
		t.Fatal(opened)
	}
}
