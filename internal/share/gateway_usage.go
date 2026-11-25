package share

import (
	"log"
	"time"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/responses"
	"github.com/hasoftware/claude-credentials-manager/v2/internal/usage"
)

// Responses API input includes cache hits. CCM stores four disjoint counters.
func recordGatewayUsage(session, model string, u responses.FinalUsageInfo, streamed bool) {
	if u.InputTokens <= 0 && u.OutputTokens <= 0 {
		return
	}
	now := time.Now().UTC()
	if !usage.IsValidSessionID(session) {
		session = "unattributed-" + now.Format("2006-01")
	}
	input := max(u.InputTokens, 0)
	cached := min(input, max(u.CacheReadInputTokens, 0))
	err := usage.Append(session, usage.Record{TS: now, Model: model, In: int64(input - cached), Out: int64(max(0, u.OutputTokens)), CR: int64(cached), Stream: streamed})
	if err != nil {
		log.Printf("cannot persist gateway usage: %v", err)
	}
}
