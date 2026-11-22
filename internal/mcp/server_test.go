package mcp_test

// audit: behavior - the MCP server negotiates initialize, lists tools and resources, and runs memory tools.
// audit: regression - an MCP client could miss tools, receive unfiltered text, or lose the engine contract.
// audit: coverage gap - no MCP server existed.
// audit: seam - the test drives Serve over in-memory frames with real engine deps in temp dirs.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/mcp"
	"github.com/dkmnx/recollect/internal/rpc"
)

type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func openTestDeps(t *testing.T) rpc.Deps {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "project")
	user := filepath.Join(base, "user")
	for _, dir := range []string{project, user} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	deps, err := rpc.OpenBindings(rpc.Bindings{
		ProjectDir: project, ProjectRoot: project, UserDir: user,
		HarnessID: "mcp", Capabilities: []string{"context"}, Protocol: rpc.Protocol{Major: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deps.Store.Close() })
	return deps
}

func frame(id int, method, params string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
}

func runFrames(t *testing.T, deps rpc.Deps, frames []string) []wireResponse {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(frames, "\n") + "\n")
	if err := mcp.Serve(in, &out, deps, "", "mcp", "test"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var replies []wireResponse
	decoder := json.NewDecoder(&out)
	for {
		var reply wireResponse
		if err := decoder.Decode(&reply); err != nil {
			break
		}
		replies = append(replies, reply)
	}
	return replies
}

func TestServerTranscript(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, []string{
		frame(1, "initialize", `{"protocolVersion":"2024-11-05","clientInfo":{"name":"test"}}`),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":4}}`,
		frame(2, "ping", `{}`),
		frame(3, "tools/list", `{}`),
		frame(4, "tools/call", `{"name":"memory_save","arguments":{"description":"Setup","body":"Run go test."}}`),
		frame(5, "tools/call", `{"name":"memory_search","arguments":{"query":"setup"}}`),
		frame(6, "tools/call", `{"name":"memory_context","arguments":{}}`),
		frame(7, "resources/read", `{"uri":"recollect://context"}`),
		frame(8, "no.such.method", `{}`),
	})
	if len(replies) != 8 {
		t.Fatalf("replies = %d, want 8", len(replies))
	}

	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := json.Unmarshal(replies[0].Result, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != "2024-11-05" || init.Capabilities["tools"] == nil || init.Capabilities["resources"] == nil {
		t.Fatalf("initialize = %+v", init)
	}

	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(replies[2].Result, &list); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, item := range list.Tools {
		names[item.Name] = true
	}
	for _, want := range []string{"memory_search", "memory_read", "memory_save", "memory_correct", "memory_delete", "memory_context"} {
		if !names[want] {
			t.Fatalf("tools missing %q: %v", want, names)
		}
	}

	save := toolText(t, replies[3])
	if !strings.Contains(save, "revision") {
		t.Fatalf("save = %q", save)
	}
	search := toolText(t, replies[4])
	if !strings.Contains(search, "Setup") {
		t.Fatalf("search = %q", search)
	}
	if contextText := toolText(t, replies[5]); !strings.Contains(contextText, "Setup") {
		t.Fatalf("context = %q", contextText)
	}

	var resource struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(replies[6].Result, &resource); err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, "Setup") {
		t.Fatalf("resource = %+v", resource)
	}

	if replies[7].Error == nil || replies[7].Error.Code != -32601 {
		t.Fatalf("unknown method = %+v", replies[7].Error)
	}
}

func toolContent(t *testing.T, reply wireResponse) (string, bool) {
	t.Helper()
	if reply.Error != nil {
		t.Fatalf("tool error: %s", reply.Error.Message)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 {
		t.Fatal("tool returned no content")
	}
	return result.Content[0].Text, result.IsError
}

func toolText(t *testing.T, reply wireResponse) string {
	t.Helper()
	text, isError := toolContent(t, reply)
	if isError {
		t.Fatalf("tool reported an error: %s", text)
	}
	return text
}

func TestUserCorrectionRequiresBody(t *testing.T) {
	deps := openTestDeps(t)
	first := runFrames(t, deps, []string{
		frame(1, "tools/call", `{"name":"memory_save","arguments":{"target":"user","description":"user memory","body":"keep me","reason":"initial"}}`),
		frame(2, "tools/call", `{"name":"memory_read","arguments":{"target":"user"}}`),
	})
	var before struct {
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(toolText(t, first[1])), &before); err != nil {
		t.Fatal(err)
	}
	second := runFrames(t, deps, []string{
		frame(3, "tools/call", fmt.Sprintf(
			`{"name":"memory_correct","arguments":{"target":"user","name":"user","description":"changed","revision":%q}}`,
			before.Revision,
		)),
		frame(4, "tools/call", `{"name":"memory_read","arguments":{"target":"user"}}`),
	})
	rejected, isError := toolContent(t, second[0])
	if !isError || !strings.Contains(rejected, "nonblank body") {
		t.Fatalf("correction = %q, isError = %v", rejected, isError)
	}
	var after struct {
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(toolText(t, second[1])), &after); err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Content != before.Content {
		t.Fatalf("user memory changed after a rejected correction")
	}
}

func TestToolErrorsArePathFree(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, []string{
		frame(1, "tools/call", `{"name":"memory_correct","arguments":{"name":"missing","revision":"stale"}}`),
		frame(2, "tools/call", `{"name":"memory_save","arguments":{"target":"global","description":"x"}}`),
		frame(3, "tools/call", `{"name":"memory_read","arguments":{}}`),
	})
	notFound, isError := toolContent(t, replies[0])
	if !isError || !strings.Contains(notFound, "was not found") {
		t.Fatalf("missing fact = %q, isError = %v", notFound, isError)
	}
	if strings.ContainsAny(notFound, `/\`) {
		t.Fatalf("missing fact leaked a path: %q", notFound)
	}
	if target, isError := toolContent(t, replies[1]); !isError || !strings.Contains(target, "target must be") {
		t.Fatalf("bad target = %q, isError = %v", target, isError)
	}
	if name, isError := toolContent(t, replies[2]); !isError || !strings.Contains(name, "name is required") {
		t.Fatalf("missing name = %q, isError = %v", name, isError)
	}
}

func TestSaveRedactsCredentials(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, []string{
		frame(1, "tools/call", `{"name":"memory_save","arguments":{"description":"API note","body":"api key: AKIAIOSFODNN7EXAMPLE"}}`),
	})
	var saved struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(toolText(t, replies[0])), &saved); err != nil {
		t.Fatal(err)
	}
	fact, err := deps.Store.ReadFact(saved.Name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fact.Body, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("credential persisted: %q", fact.Body)
	}
}
