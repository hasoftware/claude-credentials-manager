// Package identity assembles per-request outbound headers for codex
// requests. Identity is fully synthesized from compile-time constants
// (mirroring OmniRoute's approach) plus credential-derived bearer +
// chatgpt-account-id. No spawn-codex-CLI capture needed.
package identity

// Static identity values sent with Codex Responses requests.
// Keep the version and user agent in sync. The backend rejects some models
// when the request reports a client version below their minimum.
//
// StaticUserAgent's platform/arch portion is intentionally hardcoded
// (mirrors OmniRoute's "(Windows 10.0.26100; x64)" approach: every
// request looks like the same host). Do NOT derive from runtime.GOOS
// or runtime.GOARCH: doing so would fingerprint each ccm install
// differently and create a unique signal chatgpt.com could
// potentially track.
const (
	StaticVersion    = "0.169.1"
	StaticOpenaiBeta = "responses=experimental"
	StaticUserAgent  = "codex-cli/" + StaticVersion + " (Linux; x86_64)"
)

// Static parity headers shipped with ccm. Mirror what the real codex
// CLI emits per OpenAI's openai/codex repo.
//
//   originator: identifies the binary type ("codex_cli_rs" for the Rust
//     CLI). chatgpt.com appears not to gate on this, but every CLI
//     request sets it.
//   X-Codex-Beta-Features: comma-separated list of beta features the
//     client supports. The current production codex CLI advertises
//     "responses_websockets". chatgpt.com tolerates plain HTTP POST
//     against the responses endpoint regardless.
const (
	StaticOriginator        = "codex_cli_rs"
	StaticCodexBetaFeatures = "responses_websockets"
)
