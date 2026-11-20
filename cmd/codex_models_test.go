package cmd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCodexLaunchSettings(t *testing.T) {
	args, env, err := codexLaunchOptions([]string{"--effort", "max"}, []string{`CLAUDE_CODE_EXTRA_BODY={"custom":true}`}, "udeep", "fast", false)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		ModelPicker struct {
			Options []struct{ Model, Label, Description string }
		}
	}
	if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.ModelPicker.Options) != 4 || args[3] != "udeep-oai" || args[5] != "max" {
		t.Fatalf("unexpected launcher args: %v", args)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, `"speed":"fast"`) || !strings.Contains(joined, `"custom":true`) {
		t.Fatalf("unexpected child env: %v", env)
	}
	for _, input := range [][2]string{{"bogus", "fast"}, {"uthink", "bogus"}} {
		if _, _, err := codexLaunchOptions(nil, nil, input[0], input[1], false); err == nil {
			t.Errorf("accepted %v", input)
		}
	}
}

func TestCodexProfilesKeepClaudeFamilies(t *testing.T) {
	for _, original := range [][]string{nil, {
		"ANTHROPIC_DEFAULT_OPUS_MODEL=existing-opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=existing-sonnet",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=existing-haiku",
	}} {
		args, env, err := codexLaunchOptions(nil, original, "uthink", "", true)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(env, original) {
			t.Fatalf("profile changed environment: got %v, want %v", env, original)
		}
		var settings struct {
			ModelPicker struct {
				ReplaceBuiltInOptions bool
				Options               []struct{ Model string }
			}
		}
		if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.ModelPicker.ReplaceBuiltInOptions || len(settings.ModelPicker.Options) != 5 || settings.ModelPicker.Options[4].Model != "grok-4.7" {
			t.Fatalf("expected four Codex rows and one additional Grok row: %s", args[1])
		}
	}
}
