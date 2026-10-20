// Command gen writes the synthetic loop-replay fixture consumed by
// internal/responses/loopreg_test.go:
//
//	go run ./internal/responses/testdata/loop_replay/gen > internal/responses/testdata/loop_replay/sample.jsonl
//
// The output mimics a CCM_TRACE=1 capture of a Claude Code session routed
// through the Responses translator: one "in.raw" line per /v1/messages
// request and one "upstream.resp.event" line carrying that turn's whole
// upstream SSE stream. Every value is invented here; nothing is derived
// from a real trace. Output is deterministic, so regenerating it must
// produce no diff.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type obj = map[string]any

// call is one tool call the upstream model makes, plus the tool_result
// Claude Code sends back on the following turn.
type call struct {
	name   string
	input  obj
	result string
}

// turn is one upstream response: optional assistant text, then tool calls.
type turn struct {
	text  string
	calls []call
}

const (
	sessionID = "00000000-0000-0000-0000-000000000001"
	cwd       = "/work/sample-app"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// loopPair is the TaskUpdate pair the model repeats once it is stuck in
// the loop: it re-marks task 1 done and task 2 started on every turn.
var loopPair = []call{
	{"TaskUpdate", obj{"taskId": "1", "status": "completed"}, "Updated task #1 status"},
	{"TaskUpdate", obj{"taskId": "2", "status": "in_progress"}, "Updated task #2 status"},
}

var script = []turn{
	{text: "I'll break this into tasks first.", calls: []call{
		{"TaskCreate", obj{"subject": "Profile the export path", "description": "Find where report export spends its time.", "activeForm": "Profiling the export path"}, "Task #1 created successfully: Profile the export path"},
		{"TaskCreate", obj{"subject": "Fix the hot path", "description": "Remove the sequential awaits that dominate the profile.", "activeForm": "Fixing the hot path"}, "Task #2 created successfully: Fix the hot path"},
	}},
	{calls: []call{
		{"TaskUpdate", obj{"taskId": "1", "status": "in_progress"}, "Updated task #1 status"},
		{"Read", obj{"file_path": cwd + "/src/export.ts"}, exportSource},
	}},
	{text: "Rows are fetched one at a time inside the loop.", calls: []call{
		{"Bash", obj{"command": `grep -rn "await fetchRow" src`, "description": "Find sequential row fetches"}, "src/export.ts:6:    const row = await fetchRow(id);\nsrc/export.ts:10:  const summary = await fetchRow(summaryId);"},
	}},
	{calls: loopPair},
	{calls: loopPair},
	{calls: loopPair},
	{calls: []call{
		{"Bash", obj{"command": "find . -type f -name '*.ts'", "description": "List TypeScript sources"}, bigListing()},
	}},
	{text: "Export now fetches rows concurrently; tasks #1 and #2 are done."},
}

// exportSource is the Read result for the invented src/export.ts, in
// Claude Code's numbered-line format.
var exportSource = numbered(`import { fetchRow, writeCsv } from "./db";

export async function exportReport(ids: string[], summaryId: string) {
  const rows = [];
  for (const id of ids) {
    const row = await fetchRow(id);
    rows.push(row);
  }

  const summary = await fetchRow(summaryId);
  return writeCsv([...rows, summary]);
}`)

func numbered(src string) string {
	var b strings.Builder
	for i, ln := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, ln)
	}
	return b.String()
}

// bigListing is a tool_result larger than the translator's 64 KiB
// tool_result cap, so the replay exercises truncation.
func bigListing() string {
	var b strings.Builder
	for i := 0; i < 2200; i++ {
		fmt.Fprintf(&b, "./src/generated/module_%04d/index.ts\n", i)
	}
	return b.String()
}

var tools = []any{
	tool("Bash", "Executes a bash command.", obj{
		"command":     obj{"type": "string"},
		"description": obj{"type": "string"},
	}, "command"),
	tool("Read", "Reads a file from the local filesystem.", obj{
		"file_path": obj{"type": "string"},
	}, "file_path"),
	tool("TaskCreate", "Creates a task in the task list.", obj{
		"subject":     obj{"type": "string"},
		"description": obj{"type": "string"},
		"activeForm":  obj{"type": "string"},
	}, "subject", "description"),
	tool("TaskUpdate", "Updates a task in the task list.", obj{
		"taskId": obj{"type": "string"},
		"status": obj{"type": "string", "enum": []any{"pending", "in_progress", "completed"}},
	}, "taskId"),
}

func tool(name, desc string, props obj, required ...string) obj {
	req := make([]any, len(required))
	for i, r := range required {
		req[i] = r
	}
	return obj{
		"name":        name,
		"description": desc,
		"input_schema": obj{
			"type":       "object",
			"properties": props,
			"required":   req,
		},
	}
}

var system = []any{
	obj{"type": "text", "text": "x-anthropic-billing-header: cc_version=0.0.0; cc_entrypoint=cli;"},
	obj{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.", "cache_control": obj{"type": "ephemeral"}},
	obj{"type": "text", "text": "Synthetic system prompt for the loop-replay fixture. Working directory: " + cwd, "cache_control": obj{"type": "ephemeral"}},
}

func main() {
	userID := string(mustJSON(obj{
		"device_id":    strings.Repeat("0", 64),
		"account_uuid": "",
		"session_id":   sessionID,
	}))
	messages := []any{
		obj{"role": "user", "content": []any{
			obj{"type": "text", "text": "Speed up the report export in the sample app."},
		}},
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	for i, t := range script {
		reqID := fmt.Sprintf("00000000-0000-7000-8000-%012d", i+1)
		body := mustJSON(obj{
			"model":              "claude-opus-4-7",
			"max_tokens":         64000,
			"stream":             true,
			"system":             system,
			"tools":              tools,
			"messages":           messages,
			"metadata":           obj{"user_id": userID},
			"thinking":           obj{"type": "adaptive"},
			"context_management": obj{"edits": []any{obj{"type": "clear_thinking_20251015", "keep": "all"}}},
			"output_config":      obj{"effort": "xhigh"},
		})
		emit(w, obj{
			"dir":     "in.raw",
			"reqId":   reqID,
			"ts":      stamp(i, 0),
			"url":     "/v1/messages",
			"headers": headers(len(body)),
			"body":    string(body),
		})
		emit(w, obj{
			"dir":   "upstream.resp.event",
			"reqId": reqID,
			"ts":    stamp(i, 1),
			"data":  sse(i, t),
		})

		// Next turn's history: the assistant turn Claude Code rendered from
		// this response, followed by the results of its tool calls.
		var assistant, results []any
		if t.text != "" {
			assistant = append(assistant, obj{"type": "text", "text": t.text})
		}
		for j, c := range t.calls {
			id := callID(i, j)
			assistant = append(assistant, obj{"type": "tool_use", "id": id, "name": c.name, "input": c.input})
			results = append(results, obj{"type": "tool_result", "tool_use_id": id, "content": c.result})
		}
		messages = append(messages, obj{"role": "assistant", "content": assistant})
		if len(results) > 0 {
			messages = append(messages, obj{"role": "user", "content": results})
		}
	}
}

func headers(contentLength int) obj {
	return obj{
		"Accept":                   "application/json",
		"Anthropic-Beta":           "claude-code-20250219,oauth-2025-04-20",
		"Anthropic-Version":        "2023-06-01",
		"Authorization":            "[REDACTED]",
		"Content-Length":           strconv.Itoa(contentLength),
		"Content-Type":             "application/json",
		"User-Agent":               "claude-cli/0.0.0 (external, cli)",
		"X-App":                    "cli",
		"X-Claude-Code-Session-Id": sessionID,
	}
}

// sse renders turn i as the upstream Responses SSE stream, in the event
// order the upstream emits: reasoning item, message text, function calls.
func sse(i int, t turn) string {
	respID := hexID("resp", i, 0)
	var b strings.Builder
	seq := 0
	ev := func(name string, data obj) {
		data["type"] = name
		data["sequence_number"] = seq
		seq++
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, mustJSON(data))
	}
	response := func(status string, usage any) obj {
		return obj{
			"id":         respID,
			"object":     "response",
			"created_at": epoch.Unix() + int64(i*10),
			"status":     status,
			"model":      "gpt-5.5",
			"output":     []any{},
			"store":      false,
			"usage":      usage,
		}
	}

	ev("response.created", obj{"response": response("in_progress", nil)})
	ev("response.in_progress", obj{"response": response("in_progress", nil)})

	out := 0
	if len(t.calls) > 0 {
		rs := obj{"id": hexID("rs", i, out), "type": "reasoning", "summary": []any{}}
		ev("response.output_item.added", obj{"item": rs, "output_index": out})
		ev("response.output_item.done", obj{"item": rs, "output_index": out})
		out++
	}
	if t.text != "" {
		id := hexID("msg", i, out)
		part := func(text string) obj {
			return obj{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": text}
		}
		message := func(status string, content []any) obj {
			return obj{"id": id, "type": "message", "status": status, "content": content, "phase": "final_answer", "role": "assistant"}
		}
		ev("response.output_item.added", obj{"item": message("in_progress", []any{}), "output_index": out})
		ev("response.content_part.added", obj{"item_id": id, "output_index": out, "content_index": 0, "part": part("")})
		for _, d := range strings.SplitAfter(t.text, " ") {
			ev("response.output_text.delta", obj{"item_id": id, "output_index": out, "content_index": 0, "delta": d, "logprobs": []any{}, "obfuscation": "synthetic"})
		}
		ev("response.output_text.done", obj{"item_id": id, "output_index": out, "content_index": 0, "logprobs": []any{}, "text": t.text})
		ev("response.content_part.done", obj{"item_id": id, "output_index": out, "content_index": 0, "part": part(t.text)})
		ev("response.output_item.done", obj{"item": message("completed", []any{part(t.text)}), "output_index": out})
		out++
	}
	for j, c := range t.calls {
		id := hexID("fc", i, out)
		args := string(mustJSON(c.input))
		fc := func(status, arguments string) obj {
			return obj{"id": id, "type": "function_call", "status": status, "arguments": arguments, "call_id": callID(i, j), "name": c.name}
		}
		ev("response.output_item.added", obj{"item": fc("in_progress", ""), "output_index": out})
		ev("response.function_call_arguments.delta", obj{"item_id": id, "output_index": out, "delta": args, "obfuscation": "synthetic"})
		ev("response.function_call_arguments.done", obj{"item_id": id, "output_index": out, "arguments": args})
		ev("response.output_item.done", obj{"item": fc("completed", args), "output_index": out})
		out++
	}

	input := 1000 + 500*i
	cached := 512 * i
	output := 40 + i
	ev("response.completed", obj{"response": response("completed", obj{
		"input_tokens":          input,
		"input_tokens_details":  obj{"cached_tokens": cached},
		"output_tokens":         output,
		"output_tokens_details": obj{"reasoning_tokens": 16},
		"total_tokens":          input + output,
	})})
	return b.String()
}

// hexID mimics the upstream's prefixed hex ids, unique per (turn, output index).
func hexID(prefix string, turn, outputIndex int) string {
	return fmt.Sprintf("%s_%048x", prefix, (turn+1)*100+outputIndex)
}

func callID(turn, j int) string {
	return fmt.Sprintf("call_synthetic%02d%02d", turn+1, j+1)
}

func stamp(turn, step int) string {
	return epoch.Add(time.Duration(turn)*10*time.Second + time.Duration(step)*time.Second).Format("2006-01-02T15:04:05.000Z")
}

func emit(w *bufio.Writer, rec obj) {
	w.Write(mustJSON(rec))
	w.WriteByte('\n')
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
