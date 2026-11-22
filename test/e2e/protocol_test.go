package e2e

// audit: behavior - a scripted transcript produces the documented wire responses, error codes, and receipt rules.
// audit: regression - docs/protocol.md could drift from the serve loop, error mapping, or revision guard.
// audit: coverage gap - no checked-in transcript pinned initialize, memory operations, errors, status, and close.
// audit: seam - the test replays newline-delimited frames against the built binary and compares full normalized responses to a golden file.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	capturedOnPattern = regexp.MustCompile(`capturedOn: \d{4}-\d{2}-\d{2}`)
	revisionPattern   = regexp.MustCompile(`"revision":\s*"([0-9a-f]{64})"`)
)

type transcriptEntry struct {
	Method   string          `json:"method"`
	Response json.RawMessage `json:"response"`
}

// jsonEscaped returns the form a path takes inside a JSON string.
func jsonEscaped(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return string(raw[1 : len(raw)-1])
}

func TestProtocolTranscript(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()

	revisions := make(map[string]string)
	normalize := func(line string) json.RawMessage {
		text := strings.ReplaceAll(line, jsonEscaped(d.project), "<project>")
		text = strings.ReplaceAll(text, jsonEscaped(d.user), "<user>")
		text = strings.ReplaceAll(text, d.project, "<project>")
		text = strings.ReplaceAll(text, d.user, "<user>")
		text = capturedOnPattern.ReplaceAllString(text, "capturedOn: <date>")
		text = revisionPattern.ReplaceAllStringFunc(text, func(match string) string {
			value := revisionPattern.FindStringSubmatch(match)[1]
			placeholder, ok := revisions[value]
			if !ok {
				placeholder = fmt.Sprintf("<revision-%d>", len(revisions)+1)
				revisions[value] = placeholder
			}
			return `"revision":"` + placeholder + `"`
		})
		return json.RawMessage(text)
	}

	send := func(method string, params any) string {
		t.Helper()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.nextID++
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`+"\n", c.nextID, method, raw)
		if _, err := io.WriteString(c.in, frame); err != nil {
			t.Fatalf("write frame: %v; stderr: %s", err, c.stderr.String())
		}
		line, err := c.out.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame (%s): %v; stderr: %s", method, err, c.stderr.String())
		}
		return line
	}

	var entries []transcriptEntry
	record := func(method string, params any) string {
		t.Helper()
		line := send(method, params)
		entries = append(entries, transcriptEntry{Method: method, Response: normalize(line)})
		return line
	}

	record("initialize", map[string]any{
		"projectDir":      d.project,
		"userDir":         d.user,
		"harnessID":       "protocol",
		"capabilities":    []string{"generation", "events", "context"},
		"generationState": "disabled",
		"protocol":        map[string]int{"major": 1, "minor": 1},
	})
	record("memory.execute", map[string]any{
		"target": "project", "operation": "append", "requestID": "protocol-append",
		"name": "protocol-fact", "description": "Protocol fact",
		"body": "The protocol transcript is stable.",
	})
	readLine := record("memory.execute", map[string]any{
		"target": "project", "operation": "read", "name": "protocol-fact",
	})
	var readResponse struct {
		Result struct {
			Revision string `json:"revision"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(readLine), &readResponse); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	record("memory.execute", map[string]any{
		"target": "project", "operation": "search", "query": "protocol", "limit": 5,
	})
	record("memory.execute", map[string]any{
		"target": "project", "operation": "correct", "requestID": "protocol-correct-missing",
		"name": "protocol-fact", "body": "changed",
	})
	record("memory.execute", map[string]any{
		"target": "project", "operation": "correct", "requestID": "protocol-correct",
		"name": "protocol-fact", "revision": readResponse.Result.Revision,
		"body": "The protocol transcript is stable after correction.",
	})
	record("memory.execute", map[string]any{
		"target": "project", "operation": "correct", "requestID": "protocol-correct-stale",
		"name": "protocol-fact", "revision": readResponse.Result.Revision,
		"body": "stale correction",
	})
	record("memory.execute", map[string]any{
		"target": "project", "operation": "delete", "requestID": "protocol-delete-missing",
		"name": "protocol-fact",
	})
	record("status.get", map[string]any{})
	record("no.such.method", map[string]any{})
	record("engine.close", map[string]any{})

	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "protocol", "transcript.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(want) != string(got) {
		t.Fatalf("protocol transcript changed; run with UPDATE_GOLDEN=1 and review the diff\n%s", got)
	}
}
