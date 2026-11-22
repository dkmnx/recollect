package rpc

// audit: behavior - the stdio server rejects malformed, uninitialized, oversized, and post-close frames with stable codes and ignores blank lines.
// audit: regression - a protocol change could accept requests before initialize, after close, or with a mismatched major version.
// audit: coverage gap - internal/rpc had only error-mapper tests; the serve loop and handshake had no unit tests.
// audit: seam - the test drives Serve over in-memory streams with real initialize bindings in temporary directories.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func serveLines(t *testing.T, deps Deps, lines ...string) ([]Response, error) {
	t.Helper()
	var in bytes.Buffer
	for _, line := range lines {
		in.WriteString(line)
		in.WriteByte('\n')
	}
	var out bytes.Buffer
	err := Serve(&in, &out, deps)

	data := out.Bytes()
	if len(data) == 0 {
		return nil, err
	}
	if data[len(data)-1] != '\n' {
		t.Fatalf("output does not end with a newline: %q", data)
	}
	var replies []Response
	for i, line := range bytes.Split(data[:len(data)-1], []byte("\n")) {
		var resp Response
		if unmarshalErr := json.Unmarshal(line, &resp); unmarshalErr != nil {
			t.Fatalf("reply %d is not valid JSON: %v (%q)", i, unmarshalErr, line)
		}
		replies = append(replies, resp)
	}
	return replies, err
}

func requireID(t *testing.T, resp Response, want int) {
	t.Helper()
	if got := string(resp.ID); got != strconv.Itoa(want) {
		t.Fatalf("reply id = %s, want %d", got, want)
	}
}

func requestLine(t *testing.T, id int, method, params string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  json.RawMessage(params),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func initializeLine(t *testing.T, id int, protocol map[string]int) string {
	t.Helper()
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	userDir := filepath.Join(base, "user")
	for _, dir := range []string{projectDir, userDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	params := map[string]any{"projectDir": projectDir, "userDir": userDir}
	if protocol != nil {
		params["protocol"] = protocol
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal initialize params: %v", err)
	}
	return requestLine(t, id, "initialize", string(raw))
}

func requireError(t *testing.T, resp Response, code int, message string) {
	t.Helper()
	if resp.Error == nil {
		t.Fatalf("reply %s has no error, want %d %q", resp.ID, code, message)
	}
	if resp.Error.Code != code || resp.Error.Message != message {
		t.Fatalf("error = %d %q, want %d %q", resp.Error.Code, resp.Error.Message, code, message)
	}
}

func TestMethodsRequireInitialize(t *testing.T) {
	methods := []string{
		"memory.execute", "context.get", "events.ingest", "jobs.next",
		"jobs.complete", "status.get", "maintenance.execute", "engine.close",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			replies, err := serveLines(t, Deps{}, requestLine(t, 1, method, "{}"))
			if err != nil {
				t.Fatalf("Serve returned error: %v", err)
			}
			if len(replies) != 1 {
				t.Fatalf("got %d replies, want 1", len(replies))
			}
			requireID(t, replies[0], 1)
			requireError(t, replies[0], CodeInvalidRequest, "initialize required")
		})
	}
}

func TestUnknownMethod(t *testing.T) {
	replies, err := serveLines(t, Deps{}, initializeLine(t, 1, nil), requestLine(t, 2, "no.such.method", "{}"))
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	requireID(t, replies[0], 1)
	requireID(t, replies[1], 2)
	requireError(t, replies[1], CodeUnknownMethod, "unknown method: no.such.method")
}

func TestDoubleInitialize(t *testing.T) {
	replies, err := serveLines(t, Deps{}, initializeLine(t, 1, nil), initializeLine(t, 2, nil))
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	requireID(t, replies[1], 2)
	requireError(t, replies[1], CodeInvalidRequest, "already initialized")
}

func TestProtocolMajorMismatch(t *testing.T) {
	replies, err := serveLines(t, Deps{}, initializeLine(t, 1, map[string]int{"major": 2, "minor": 0}))
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(replies))
	}
	requireID(t, replies[0], 1)
	requireError(t, replies[0], CodeProtocol, "protocol major 1 required")
}

func TestMinorVersionDifferenceIsReported(t *testing.T) {
	replies, err := serveLines(t, Deps{}, initializeLine(t, 1, map[string]int{"major": 1, "minor": 3}))
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 1 || replies[0].Error != nil {
		t.Fatalf("initialize failed: %+v", replies)
	}
	requireID(t, replies[0], 1)
	var result initResult
	if err := json.Unmarshal(replies[0].Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Protocol.Major != 1 || result.Protocol.Minor != 3 {
		t.Fatalf("protocol = %+v, want 1.3", result.Protocol)
	}
	if result.Note != "minor version difference reported" {
		t.Fatalf("note = %q, want a minor-difference note", result.Note)
	}
}

func TestMalformedFrames(t *testing.T) {
	replies, err := serveLines(t, Deps{}, `{`, `{"jsonrpc":"2.0","id":7,"method":`)
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	for i, resp := range replies {
		requireError(t, resp, CodeInvalidRequest, "invalid request frame")
		if string(resp.ID) != "null" {
			t.Fatalf("reply %d id = %s, want null", i, resp.ID)
		}
	}
}

func TestOversizedFrame(t *testing.T) {
	huge := `{"jsonrpc":"2.0","id":1,"method":"` + strings.Repeat("a", maxFrameBytes) + `"}`
	_, err := serveLines(t, Deps{}, huge)
	if err == nil {
		t.Fatal("Serve accepted a frame larger than maxFrameBytes")
	}
}

func TestBlankLinesAreIgnored(t *testing.T) {
	replies, err := serveLines(t, Deps{}, "", "   ", initializeLine(t, 1, nil))
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(replies))
	}
	requireID(t, replies[0], 1)
	if replies[0].Error != nil {
		t.Fatalf("initialize failed: %+v", replies[0].Error)
	}
}

func TestRequestAfterClose(t *testing.T) {
	replies, err := serveLines(t, Deps{},
		initializeLine(t, 1, nil),
		requestLine(t, 2, "engine.close", "{}"),
		requestLine(t, 3, "status.get", "{}"),
	)
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 3 {
		t.Fatalf("got %d replies, want 3", len(replies))
	}
	for i, resp := range replies {
		requireID(t, resp, i+1)
	}
	if replies[1].Error != nil {
		t.Fatalf("engine.close failed: %+v", replies[1].Error)
	}
	requireError(t, replies[2], CodeInvalidRequest, "engine closed")
}

func TestLastErrorCodeCarriesTheLastErrorMessage(t *testing.T) {
	replies, err := serveLines(t, Deps{},
		initializeLine(t, 1, nil),
		requestLine(t, 2, "no.such.method", "{}"),
		requestLine(t, 3, "status.get", "{}"),
	)
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if len(replies) != 3 {
		t.Fatalf("got %d replies, want 3", len(replies))
	}
	for i, resp := range replies {
		requireID(t, resp, i+1)
	}
	var status statusResult
	if err := json.Unmarshal(replies[2].Result, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if want := fmt.Sprintf("unknown method: no.such.method"); status.LastErrorCode != want {
		t.Fatalf("lastErrorCode = %q, want %q", status.LastErrorCode, want)
	}
}
