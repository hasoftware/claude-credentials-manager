# Loop replay fixture

Synthetic trace in the `CCM_TRACE=1` JSONL format, modelled on a Claude
Code session that fell into a TaskUpdate loop through the Responses
translator. It is generated, not captured — every value comes from
`gen/main.go`:

```bash
go run ./internal/responses/testdata/loop_replay/gen > internal/responses/testdata/loop_replay/sample.jsonl
```

The generator is deterministic; regenerating must produce no diff.

- 8 inbound `/v1/messages` requests (`in.raw`), each paired by `reqId`
  with one `upstream.resp.event` line holding that turn's whole upstream
  SSE stream.
- Turns 4–6 repeat the same pair of TaskUpdate calls — the loop.
- Turn 7's `find` output is larger than the 64 KiB tool_result cap, so
  the final request exercises truncation.
- Every upstream turn carries a distinct `response.id`.

`loopreg_test.go` replays the requests through `TranslateRequest`
(tool_result cap) and the streams through `StreamTranslator` (distinct
`message_start` ids per turn).

Never commit a real trace here: a capture records full request bodies —
system prompt, file contents, tool output — plus account and client
identifiers. Only headers are redacted at write time.
