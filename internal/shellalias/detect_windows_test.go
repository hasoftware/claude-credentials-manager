//go:build windows

package shellalias

import (
	"os/exec"
	"testing"
)

// These tests cover detect_windows.go: the hint is "pwsh" whenever
// either PowerShell host is on PATH, regardless of $SHELL.

func stubLookPath(t *testing.T, available ...string) {
	t.Helper()
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	set := map[string]bool{}
	for _, name := range available {
		set[name] = true
	}
	lookPath = func(name string) (string, error) {
		if set[name] {
			return `C:\bin\` + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestCurrentShellHint_Windows_Pwsh(t *testing.T) {
	stubLookPath(t, "pwsh")
	if got := currentShellHint(); got != "pwsh" {
		t.Fatalf("got %q", got)
	}
}

func TestCurrentShellHint_Windows_WindowsPowerShellOnly(t *testing.T) {
	stubLookPath(t, "powershell.exe")
	if got := currentShellHint(); got != "pwsh" {
		t.Fatalf("got %q", got)
	}
}

func TestCurrentShellHint_Windows_NoPowerShell(t *testing.T) {
	stubLookPath(t, "bash")
	t.Setenv("SHELL", "/usr/bin/bash") // ignored on Windows
	if got := currentShellHint(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestDetect_Windows_PwshMovedFirst(t *testing.T) {
	stubLookPath(t, "bash", "zsh", "fish", "pwsh")
	got := Detect()
	if len(got) != 4 || got[0].Name() != "pwsh" {
		t.Fatalf("pwsh should be first of 4; got %+v", got)
	}
}
