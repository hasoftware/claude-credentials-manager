package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	grokmw "github.com/hasoftware/claude-credentials-manager/v2/internal/grok/middleware"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/codex/models"
)

var launchCodexModel, launchCodexSpeed string
var launchWithGrok bool

// Native executables preserve quoted JSON settings on Windows even when
// a shell wrapper called claude.cmd also appears earlier on PATH.
func claudeForLaunch() (string, error) {
	if runtime.GOOS == "windows" && (launchCodexModel != "" || launchCodexSpeed != "" || launchWithGrok) {
		if path, err := exec.LookPath("claude.exe"); err == nil {
			return path, nil
		}
	}
	return exec.LookPath("claude")
}

// codexLaunchOptions builds per-child settings. No account or machine paths
// are stored, and the parent shell's environment is never changed.
func codexLaunchOptions(args, env []string, model, speed string, withGrok bool) ([]string, []string, error) {
	if model == "" && speed == "" && !withGrok {
		return args, env, nil
	}
	if model == "" {
		model = "uthink"
	}
	profile, ok := models.ByAlias(model)
	if !ok {
		return nil, nil, fmt.Errorf("unknown Codex profile %q (use uthink, udeep, ulens or udash)", model)
	}
	rows := make([]map[string]string, 0, len(models.Catalog))
	for _, p := range models.Catalog {
		rows = append(rows, map[string]string{
			"model": p.ID + "-oai", "label": p.Label, "description": p.Description,
			"behavesAs": "claude-opus-4-8",
		})
	}
	if withGrok {
		rows = append(rows, map[string]string{"model": grokmw.DefaultModel, "label": "Grok · xAI", "description": "Grok 4.7 · Trò chuyện và lập trình qua tài khoản xAI"})
	}
	settings, err := json.Marshal(map[string]any{"modelPicker": map[string]any{"options": rows}})
	if err != nil {
		return nil, nil, err
	}
	if speed != "" {
		if speed != "standard" && speed != "fast" {
			return nil, nil, fmt.Errorf("unsupported Codex speed %q (use standard or fast)", speed)
		}
		body := map[string]json.RawMessage{}
		for _, entry := range env {
			key, value, _ := strings.Cut(entry, "=")
			if strings.EqualFold(key, "CLAUDE_CODE_EXTRA_BODY") && value != "" {
				if err := json.Unmarshal([]byte(value), &body); err != nil || body == nil {
					return nil, nil, fmt.Errorf("CLAUDE_CODE_EXTRA_BODY must be a JSON object")
				}
			}
		}
		body["speed"], _ = json.Marshal(speed)
		extra, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		env = append(filterEnv(env, "CLAUDE_CODE_EXTRA_BODY"), "CLAUDE_CODE_EXTRA_BODY="+string(extra))
	}
	args = append([]string{"--settings", string(settings), "--model", profile.ID + "-oai"}, args...)
	return args, env, nil
}
