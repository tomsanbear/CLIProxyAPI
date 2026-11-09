package archive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The golden fixtures under testdata/wal are the contract shared with
// cliproxyapi-archiver (spec section 10). TestFixtures verifies that this
// package reproduces manifest.json exactly; UPDATE_ARCHIVE_FIXTURES=1
// regenerates the directory, which is a format change that must be mirrored
// into the archiver repository.

const fixtureDir = "testdata/wal"

type fixtureManifest struct {
	V        int                       `json:"v"`
	Segments map[string]fixtureSegment `json:"segments"`
	Ignored  []string                  `json:"ignored"`
}

type fixtureSegment struct {
	Stats   ReadStats       `json:"stats"`
	Records []fixtureRecord `json:"records"`
}

type fixtureRecord struct {
	RequestID      string `json:"request_id"`
	Attempt        int    `json:"attempt"`
	Format         string `json:"format"`
	MetaSHA256     string `json:"meta_sha256"`
	RequestSHA256  string `json:"request_sha256"`
	ResponseSHA256 string `json:"response_sha256"`
}

func TestFixtures(t *testing.T) {
	if os.Getenv("UPDATE_ARCHIVE_FIXTURES") == "1" {
		writeFixtures(t)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(fixtureDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v (run with UPDATE_ARCHIVE_FIXTURES=1 to generate)", err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".wal"):
			want, ok := manifest.Segments[name]
			if !ok {
				t.Errorf("%s: not in manifest", name)
				continue
			}
			seen[name] = true
			f, err := os.Open(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatal(err)
			}
			records, stats, err := ReadSegment(f, dec)
			_ = f.Close()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got := summarizeSegment(t, records, stats)
			if gotJSON, wantJSON := mustJSON(t, got), mustJSON(t, want); gotJSON != wantJSON {
				t.Errorf("%s: decoded segment differs from manifest\n got: %s\nwant: %s", name, gotJSON, wantJSON)
			}
		case strings.HasSuffix(name, ".wal.open"):
			if !contains(manifest.Ignored, name) {
				t.Errorf("%s: open segment not listed under ignored", name)
			}
		}
	}
	for name := range manifest.Segments {
		if !seen[name] {
			t.Errorf("%s: in manifest but missing from %s", name, fixtureDir)
		}
	}
}

func summarizeSegment(t *testing.T, records []Record, stats ReadStats) fixtureSegment {
	t.Helper()
	seg := fixtureSegment{Stats: stats, Records: make([]fixtureRecord, 0, len(records))}
	for _, rec := range records {
		var meta Meta
		if err := json.Unmarshal(rec.Meta, &meta); err != nil {
			t.Fatalf("record meta: %v", err)
		}
		seg.Records = append(seg.Records, fixtureRecord{
			RequestID:      meta.RequestID,
			Attempt:        meta.Attempt,
			Format:         meta.Request.Format,
			MetaSHA256:     sha256Hex(rec.Meta),
			RequestSHA256:  sha256Hex(rec.Request),
			ResponseSHA256: sha256Hex(rec.Response),
		})
	}
	return seg
}

func writeFixtures(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(fixtureDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	manifest := fixtureManifest{V: 1, Segments: map[string]fixtureSegment{}}
	write := func(name string, frames ...[]byte) {
		var buf []byte
		for _, frame := range frames {
			buf = append(buf, frame...)
		}
		if err := os.WriteFile(filepath.Join(fixtureDir, name), buf, 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".wal") {
			records, stats, err := ReadSegment(strings.NewReader(string(buf)), dec)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Segments[name] = summarizeSegment(t, records, stats)
		} else {
			manifest.Ignored = append(manifest.Ignored, name)
		}
	}
	encode := func(rec Record) []byte {
		frame, err := enc.Encode(rec)
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}
	fx := newFixtureRecords()

	// seg 1: three complete frames across three formats.
	write("seg-000000000001.wal", encode(fx.claudeNative), encode(fx.openAIChat), encode(fx.gemini))

	// seg 2: failover pair, a Responses turn, then a frame cut mid-payload.
	truncated := encode(fx.claudeNative)
	truncated = truncated[:HeaderLen+len(truncated[HeaderLen:])/2]
	write("seg-000000000002.wal", encode(fx.rateLimited), encode(fx.rateLimitedRetry), encode(fx.responses), truncated)

	// seg 3: unknown version and bad CRC between two valid frames.
	unknownVersion := encode(fx.openAIChat)
	unknownVersion[4] = 99
	badCRC := encode(fx.gemini)
	badCRC[HeaderLen] ^= 0xFF
	write("seg-000000000003.wal", encode(fx.claudeNative), unknownVersion, badCRC, encode(fx.responses))

	// seg 4: in progress; readers ignore it.
	write("seg-000000000004.wal.open", encode(fx.gemini))

	// seg 5: a bad record (record_len disagrees), then a header whose payload_len
	// is over the limit, which ends the segment as corrupt before the last frame.
	badRecord := encode(fx.openAIChat)
	binary.LittleEndian.PutUint32(badRecord[12:16], binary.LittleEndian.Uint32(badRecord[12:16])+1)
	oversized := encode(fx.gemini)
	binary.LittleEndian.PutUint32(oversized[8:12], MaxPayloadLen+1)
	write("seg-000000000005.wal", encode(fx.claudeNative), badRecord, encode(fx.responses), oversized, encode(fx.claudeNative))

	// seg 6: at least a header's worth of bytes that do not start with the magic.
	write("seg-000000000006.wal", []byte("this is not a wal segment at all\n"))

	sort.Strings(manifest.Ignored)
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "manifest.json"), append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// fixtureRecords are synthetic turns in each inbound format the proxy routes.
// Nothing here is real conversation content.
type fixtureRecords struct {
	claudeNative     Record
	openAIChat       Record
	gemini           Record
	responses        Record
	rateLimited      Record
	rateLimitedRetry Record
}

func newFixtureRecords() fixtureRecords {
	baseMeta := func(requestID string, attempt int, ts, done string) Meta {
		return Meta{
			V: 1, RequestID: requestID, Attempt: attempt, TS: ts, CompletedAt: done,
			Host: "fixture-host", ProxyVersion: "fixture",
			Client: MetaClient{
				Method: "POST", RemoteAddr: "127.0.0.1",
				Headers:         map[string][]string{"content-type": {"application/json"}},
				CallerKeySHA256: sha256Hex([]byte("fixture-access-key")),
			},
		}
	}
	record := func(meta Meta, request, response string, format, kind, contentType string) Record {
		meta.Request.Format = format
		meta.Request.ContentType = "application/json"
		meta.Request.Bytes = int64(len(request))
		meta.Request.SHA256 = sha256Hex([]byte(request))
		meta.Response.Kind = kind
		meta.Response.ContentType = contentType
		meta.Response.Bytes = int64(len(response))
		meta.Response.SHA256 = sha256Hex([]byte(response))
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			panic(err)
		}
		return Record{Meta: metaJSON, Request: []byte(request), Response: []byte(response)}
	}

	var fx fixtureRecords

	claudeMeta := baseMeta("0f1e2d3c-0001-4000-8000-000000000001", 1, "2026-11-09T18:04:11.123456789Z", "2026-11-09T18:04:13.001000000Z")
	claudeMeta.Client.Path = "/v1/messages"
	claudeMeta.Client.Query = "beta=true"
	claudeMeta.Client.UserAgent = "claude-cli/2.1.319 (external, cli)"
	claudeMeta.Client.Headers["anthropic-beta"] = []string{"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"}
	claudeMeta.Client.Headers["x-app"] = []string{"cli"}
	claudeMeta.Client.Headers["x-claude-code-session-id"] = []string{"3f2e1d0c-aaaa-4bbb-8ccc-000000000001"}
	claudeMeta.Session = MetaSession{SessionID: "claude:3f2e1d0c-aaaa-4bbb-8ccc-000000000001", ClientType: "claude", Native: true}
	claudeMeta.Route = MetaRoute{Provider: "claude", RequestedModel: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5", AccountID: "claude-00000000-fixture@example.com.json", AuthType: "oauth", BaseURL: "https://api.anthropic.com"}
	claudeMeta.Outcome = MetaOutcome{Status: 200, UpstreamStatus: 200, StopReason: "end_turn", LatencyMS: 1878, TTFTMS: 412}
	claudeMeta.Usage = MetaUsage{InputTokens: 12, OutputTokens: 9, CacheReadTokens: 1480, CacheCreationTokens: 230, CacheCreation1hTokens: 230}
	fx.claudeNative = record(claudeMeta, claudeRequest, claudeSSE, "claude", "sse", "text/event-stream")

	chatMeta := baseMeta("0f1e2d3c-0002-4000-8000-000000000002", 1, "2026-11-09T18:05:00.000000000Z", "2026-11-09T18:05:01.250000000Z")
	chatMeta.Client.Path = "/v1/chat/completions"
	chatMeta.Client.UserAgent = "fixture-sdk/1.0"
	chatMeta.Session = MetaSession{SessionID: "derived:ctx:v1:" + strings.Repeat("ab", 32), ClientType: "lcp"}
	chatMeta.Route = MetaRoute{Provider: "claude", RequestedModel: "claude-sonnet-5-5", UpstreamModel: "claude-sonnet-5-5", AccountID: "claude-00000000-fixture@example.com.json", AuthType: "oauth", BaseURL: "https://api.anthropic.com"}
	chatMeta.Outcome = MetaOutcome{Status: 200, UpstreamStatus: 200, StopReason: "stop", LatencyMS: 1250, TTFTMS: 1250}
	chatMeta.Usage = MetaUsage{InputTokens: 31, OutputTokens: 7}
	fx.openAIChat = record(chatMeta, openAIChatRequest, openAIChatResponse, "openai-chat", "json", "application/json")

	geminiMeta := baseMeta("0f1e2d3c-0003-4000-8000-000000000003", 1, "2026-11-09T18:06:00.000000000Z", "2026-11-09T18:06:00.900000000Z")
	geminiMeta.Client.Path = "/v1beta/models/gemini-2.5-pro:generateContent"
	geminiMeta.Client.UserAgent = "fixture-gemini-cli/0.1"
	geminiMeta.Session = MetaSession{SessionID: "derived:ctx:v1:" + strings.Repeat("cd", 32), ClientType: "lcp"}
	geminiMeta.Route = MetaRoute{Provider: "gemini", RequestedModel: "gemini-2.5-pro", UpstreamModel: "gemini-2.5-pro", AccountID: "gemini-fixture.json", AuthType: "oauth", BaseURL: "https://generativelanguage.googleapis.com"}
	geminiMeta.Outcome = MetaOutcome{Status: 200, UpstreamStatus: 200, StopReason: "STOP", LatencyMS: 900, TTFTMS: 900}
	geminiMeta.Usage = MetaUsage{InputTokens: 14, OutputTokens: 5}
	fx.gemini = record(geminiMeta, geminiRequest, geminiResponse, "gemini", "json", "application/json")

	responsesMeta := baseMeta("0f1e2d3c-0004-4000-8000-000000000004", 1, "2026-11-09T18:07:00.000000000Z", "2026-11-09T18:07:02.000000000Z")
	responsesMeta.Client.Path = "/v1/responses"
	responsesMeta.Client.UserAgent = "codex_cli_rs/0.50.0"
	responsesMeta.Client.Headers["session_id"] = []string{"019a0000-0000-7000-8000-000000000004"}
	responsesMeta.Session = MetaSession{SessionID: "codex:019a0000-0000-7000-8000-000000000004", ClientType: "codex"}
	responsesMeta.Route = MetaRoute{Provider: "codex", RequestedModel: "gpt-5.1-codex", UpstreamModel: "gpt-5.1-codex", AccountID: "codex-fixture.json", AuthType: "oauth", BaseURL: "https://chatgpt.com/backend-api"}
	responsesMeta.Outcome = MetaOutcome{Status: 200, UpstreamStatus: 200, StopReason: "completed", LatencyMS: 2000, TTFTMS: 300}
	responsesMeta.Usage = MetaUsage{InputTokens: 20, OutputTokens: 6, ReasoningTokens: 4}
	fx.responses = record(responsesMeta, responsesRequest, responsesSSE, "openai-responses", "sse", "text/event-stream")

	rlMeta := baseMeta("0f1e2d3c-0005-4000-8000-000000000005", 1, "2026-11-09T18:08:00.000000000Z", "2026-11-09T18:08:00.150000000Z")
	rlMeta.Client.Path = "/v1/messages"
	rlMeta.Client.UserAgent = "claude-cli/2.1.319 (external, cli)"
	rlMeta.Client.Headers["x-claude-code-session-id"] = []string{"3f2e1d0c-aaaa-4bbb-8ccc-000000000005"}
	rlMeta.Session = MetaSession{SessionID: "claude:3f2e1d0c-aaaa-4bbb-8ccc-000000000005", ClientType: "claude", Native: true}
	rlMeta.Route = MetaRoute{Provider: "claude", RequestedModel: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5", AccountID: "claude-11111111-fixture-a@example.com.json", AuthType: "oauth", BaseURL: "https://api.anthropic.com"}
	rlMeta.Outcome = MetaOutcome{Status: 0, UpstreamStatus: 429, Failed: true, ErrorCode: "rate_limit_error", ErrorMessage: "This request would exceed your account's rate limit. Please try again later.", LatencyMS: 150}
	fx.rateLimited = record(rlMeta, claudeRequest, "", "claude", "none", "")

	retryMeta := rlMeta
	retryMeta.Attempt = 2
	retryMeta.TS = "2026-11-09T18:08:00.000000000Z"
	retryMeta.CompletedAt = "2026-11-09T18:08:02.400000000Z"
	retryMeta.Route.AccountID = "claude-22222222-fixture-b@example.com.json"
	retryMeta.Outcome = MetaOutcome{Status: 200, UpstreamStatus: 200, StopReason: "end_turn", LatencyMS: 2250, TTFTMS: 500}
	retryMeta.Usage = MetaUsage{InputTokens: 12, OutputTokens: 9, CacheCreationTokens: 1710, CacheCreation1hTokens: 1710}
	fx.rateLimitedRetry = record(retryMeta, claudeRequest, claudeSSE, "claude", "sse", "text/event-stream")

	return fx
}

const claudeRequest = `{"model":"claude-opus-5-5","max_tokens":32000,"stream":true,"thinking":{"type":"adaptive"},"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.319.abc; cc_entrypoint=cli; cch=00000;"},{"type":"text","text":"You are a fixture assistant for the archive WAL test suite.","cache_control":{"type":"ephemeral","ttl":"1h"}}],"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}},{"name":"Bash","description":"Run a command","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]},"cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"List the files in the fixture directory."}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_fixture_0001","name":"Bash","input":{"command":"ls"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fixture_0001","content":"manifest.json\nseg-000000000001.wal\n","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],"metadata":{"user_id":"{\"device_id\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"account_uuid\":\"00000000-0000-4000-8000-000000000000\",\"session_id\":\"3f2e1d0c-aaaa-4bbb-8ccc-000000000001\"}"}}`

const claudeSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture_0001\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-5-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":1480,\"cache_creation_input_tokens\":230,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Two files: manifest.json and one segment.\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":9}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

const openAIChatRequest = `{"model":"claude-sonnet-5-5","messages":[{"role":"system","content":"You are a fixture assistant."},{"role":"user","content":"Say the word fixture."}],"tools":[{"type":"function","function":{"name":"noop","description":"Does nothing","parameters":{"type":"object","properties":{}}}}],"stream":false}`

const openAIChatResponse = `{"id":"chatcmpl-fixture-0002","object":"chat.completion","created":1794160000,"model":"claude-sonnet-5-5","choices":[{"index":0,"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":31,"completion_tokens":7,"total_tokens":38}}`

const geminiRequest = `{"systemInstruction":{"parts":[{"text":"You are a fixture assistant."}]},"contents":[{"role":"user","parts":[{"text":"Reply with the word fixture."}]}],"tools":[{"functionDeclarations":[{"name":"noop","description":"Does nothing","parameters":{"type":"OBJECT","properties":{}}}]}],"generationConfig":{"maxOutputTokens":64}}`

const geminiResponse = `{"candidates":[{"content":{"role":"model","parts":[{"text":"fixture"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":14,"candidatesTokenCount":5,"totalTokenCount":19},"modelVersion":"gemini-2.5-pro"}`

const responsesRequest = `{"model":"gpt-5.1-codex","instructions":"You are a fixture assistant.","input":[{"role":"user","content":[{"type":"input_text","text":"Reply with the word fixture."}]}],"tools":[{"type":"function","name":"noop","description":"Does nothing","parameters":{"type":"object","properties":{}}}],"stream":true,"store":false,"reasoning":{"effort":"low"}}`

const responsesSSE = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture_0004\",\"status\":\"in_progress\"}}\n\n" +
	"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_fixture_0004\",\"output_index\":0,\"content_index\":0,\"delta\":\"fixture\"}\n\n" +
	"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture_0004\",\"status\":\"completed\",\"usage\":{\"input_tokens\":20,\"output_tokens\":6,\"output_tokens_details\":{\"reasoning_tokens\":4}}}}\n\n"
