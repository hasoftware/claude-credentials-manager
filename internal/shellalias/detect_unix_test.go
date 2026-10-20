//go:build !windows

package shellalias

import "testing"

// These tests cover the $SHELL-based hint in detect_unix.go.

func TestDetect_CurrentShellHintAtIndexZero(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	t.Setenv("SHELL", "/bin/zsh")
	got := Detect()
	if len(got) == 0 || got[0].Name() != "zsh" {
		t.Fatalf("zsh should be first; got %+v", got)
	}
}

func TestDetect_NoCurrentShellNoReorder(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	t.Setenv("SHELL", "/bin/csh") // unknown
	got := Detect()
	if got[0].Name() != "bash" {
		t.Fatalf("expected default order (bash first), got %+v", got)
	}
}

func TestCurrentShellHint_EmptyShell(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := currentShellHint(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCurrentShellHint_PwshFromShellEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/pwsh")
	if got := currentShellHint(); got != "pwsh" {
		t.Fatalf("got %q", got)
	}
}

func TestDetect_HintAlreadyFirstNoOpSwap(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	t.Setenv("SHELL", "/bin/bash") // bash is already first in default order
	got := Detect()
	if got[0].Name() != "bash" {
		t.Fatalf("expected bash first, got %+v", got)
	}
}
