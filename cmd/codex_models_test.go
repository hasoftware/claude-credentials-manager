package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexLaunchSettings(t *testing.T) {
	args, env, err := codexLaunchOptions([]string{"--effort", "max"}, []string{`CLAUDE_CODE_EXTRA_BODY={"custom":true}`}, "udeep", "fast")
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
		if _, _, err := codexLaunchOptions(nil, nil, input[0], input[1]); err == nil {
			t.Errorf("accepted %v", input)
		}
	}
}
