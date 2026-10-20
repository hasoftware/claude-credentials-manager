package shellalias

import (
	"os/exec"
	"sort"
	"testing"
)

func TestDetect_ReturnsBuiltins(t *testing.T) {
	// Replace the LookPath hook so we get deterministic output regardless
	// of what's actually installed on the test host.
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	available := map[string]bool{"bash": true, "zsh": true, "fish": false, "pwsh": false}
	lookPath = func(name string) (string, error) {
		if available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	got := Detect()
	if len(got) != 2 {
		t.Fatalf("got %d shells", len(got))
	}
	names := []string{got[0].Name(), got[1].Name()}
	sort.Strings(names)
	if names[0] != "bash" || names[1] != "zsh" {
		t.Fatalf("got %v", names)
	}
}

func TestDetect_NoShellsPresent(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		return "", exec.ErrNotFound
	}
	got := Detect()
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}
