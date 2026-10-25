package cmd

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/shellalias"
)

func TestAliasDispatch_Create_UsesInvokedLauncher(t *testing.T) {
	resetAliasHooks()
	t.Cleanup(resetAliasHooks)

	aliasLauncherFn = func() string { return "ccm2" }
	aliasDetectFn = func() []shellalias.Shell { return nil }
	var got string
	aliasInstallFn = func(name string, payload []string, targets []shellalias.Shell, force bool) []error {
		got = shellalias.Launcher
		return make([]error, len(targets))
	}
	err := runAlias(io.Discard, io.Discard, []string{"--as", "cld", "--shells", "bash", "--load-balance", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ccm2" {
		t.Fatalf("Launcher during install = %q, want ccm2", got)
	}
}

func TestInvokedLauncher(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dir := t.TempDir()
	// A hard link is the same file as the running test binary, which is
	// what a renamed install looks like from inside the process.
	if err := os.Link(self, filepath.Join(dir, "ccm2"+exe)); err != nil {
		t.Skipf("hard link unsupported here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ccm3"+exe), []byte("another binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	orig := os.Args[0]
	t.Cleanup(func() { os.Args[0] = orig })

	cases := []struct{ argv0, want string }{
		{filepath.Join(dir, "ccm2"+exe), "ccm2"},
		{"ccm2", "ccm2"},
		{"ccm3", "ccm"}, // on PATH, but a different file
		{"ccm4", "ccm"}, // not on PATH
		{"ccm", "ccm"},
		{"bad;name", "ccm"},
	}
	for _, tc := range cases {
		os.Args[0] = tc.argv0
		if got := invokedLauncher(); got != tc.want {
			t.Errorf("argv0 %q: got %q want %q", tc.argv0, got, tc.want)
		}
	}
}
