package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/codex/models"
)

var launchCodexModel, launchCodexSpeed string

// Native executables preserve quoted JSON settings on Windows even when
// a shell wrapper called claude.cmd also appears earlier on PATH.
func claudeForLaunch() (string, error) {
	if runtime.GOOS == "windows" && (launchCodexModel != "" || launchCodexSpeed != "") {
		if path, err := exec.LookPath("claude.exe"); err == nil {
			return path, nil
		}
	}
	return exec.LookPath("claude")
}

// codexLaunchOptions builds per-child settings. No account or machine paths
// are stored, and the parent shell's environment is never changed.
func codexLaunchOptions(args, env []string, model, speed string) ([]string, []string, error) {
	if model == "" && speed == "" {
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
	settings, err := json.Marshal(map[string]any{"modelPicker": map[string]any{"options": rows}})
	if err != nil {
		return nil, nil, err
	}
	// Background Claude Code tasks can select a family alias independently
	// of --model. Route them through the same Codex gateway.
	for _, family := range []struct{ key, model string }{
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "udash-oai"},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", "uthink-oai"},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", "udeep-oai"},
	} {
		env = append(filterEnv(env, family.key), family.key+"="+family.model)
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
