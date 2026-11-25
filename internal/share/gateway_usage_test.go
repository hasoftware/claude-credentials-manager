package share

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/responses"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/usage"
)

func TestGatewayUsageSeparatesCacheAndRejectsUnsafeSession(t *testing.T) {
	t.Setenv("CCM_HOME", t.TempDir())
	recordGatewayUsage("../invalid", "model", responses.FinalUsageInfo{InputTokens: 100, CacheReadInputTokens: 60, OutputTokens: 10}, true)
	recordGatewayUsage("../invalid", "model", responses.FinalUsageInfo{}, true)
	files, err := os.ReadDir(usage.Dir())
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	records, err := usage.LoadFile(filepath.Join(usage.Dir(), files[0].Name()))
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	r := records[0]
	if r.In != 40 || r.CR != 60 || r.Out != 10 || r.Model != "model" || !r.Stream {
		t.Fatal(r)
	}
}
