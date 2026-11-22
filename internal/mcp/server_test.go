package mcp_test

// audit: behavior - the MCP server negotiates initialize, lists tools and resources, and runs memory tools.
// audit: regression - an MCP client could miss tools, receive unfiltered text, or lose the engine contract.
// audit: coverage gap - no MCP server existed.
// audit: seam - the test drives Serve over in-memory frames with real engine deps in temp dirs.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

func runWith(t *testing.T, opts mcp.Options, frames []string) []wireResponse {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(frames, "\n") + "\n")
	if err := mcp.Serve(in, &out, opts); err != nil {
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

func runFrames(t *testing.T, deps rpc.Deps, frames []string) []wireResponse {
	t.Helper()
	return runWith(t, mcp.Options{
		Harness: "mcp", Version: "test",
		Open: func(string) (mcp.Opened, error) { return mcp.Opened{Deps: deps}, nil },
	}, frames)
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

func withInit(frames ...string) []string {
	return append([]string{
		frame(99, "initialize", `{"protocolVersion":"2024-11-05"}`),
	}, frames...)
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
	first := runFrames(t, deps, withInit(
		frame(1, "tools/call", `{"name":"memory_save","arguments":{"target":"user","description":"user memory","body":"keep me","reason":"initial"}}`),
		frame(2, "tools/call", `{"name":"memory_read","arguments":{"target":"user"}}`),
	))
	var before struct {
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(toolText(t, first[2])), &before); err != nil {
		t.Fatal(err)
	}
	second := runFrames(t, deps, withInit(
		frame(3, "tools/call", fmt.Sprintf(
			`{"name":"memory_correct","arguments":{"target":"user","name":"user","description":"changed","revision":%q}}`,
			before.Revision,
		)),
		frame(4, "tools/call", `{"name":"memory_read","arguments":{"target":"user"}}`),
	))
	rejected, isError := toolContent(t, second[1])
	if !isError || !strings.Contains(rejected, "nonblank body") {
		t.Fatalf("correction = %q, isError = %v", rejected, isError)
	}
	var after struct {
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(toolText(t, second[2])), &after); err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Content != before.Content {
		t.Fatalf("user memory changed after a rejected correction")
	}
}

func TestToolErrorsArePathFree(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, withInit(
		frame(1, "tools/call", `{"name":"memory_correct","arguments":{"name":"missing","revision":"stale"}}`),
		frame(2, "tools/call", `{"name":"memory_save","arguments":{"target":"global","description":"x"}}`),
		frame(3, "tools/call", `{"name":"memory_read","arguments":{}}`),
	))
	notFound, isError := toolContent(t, replies[1])
	if !isError || !strings.Contains(notFound, "was not found") {
		t.Fatalf("missing fact = %q, isError = %v", notFound, isError)
	}
	if strings.ContainsAny(notFound, `/\`) {
		t.Fatalf("missing fact leaked a path: %q", notFound)
	}
	if target, isError := toolContent(t, replies[2]); !isError || !strings.Contains(target, "target must be") {
		t.Fatalf("bad target = %q, isError = %v", target, isError)
	}
	if name, isError := toolContent(t, replies[3]); !isError || !strings.Contains(name, "name is required") {
		t.Fatalf("missing name = %q, isError = %v", name, isError)
	}
}

func TestRootsNegotiation(t *testing.T) {
	rooted := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rooted, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rooted, "user"), 0o700); err != nil {
		t.Fatal(err)
	}
	opened := make(chan string, 2)
	opts := mcp.Options{
		ProjectHint: t.TempDir(), Harness: "roots", Version: "test",
		Open: func(dir string) (mcp.Opened, error) {
			opened <- dir
			if err := os.MkdirAll(filepath.Join(dir, "user"), 0o700); err != nil {
				return mcp.Opened{}, err
			}
			deps, err := rpc.OpenBindings(rpc.Bindings{
				ProjectDir: dir, ProjectRoot: dir, UserDir: filepath.Join(dir, "user"),
				HarnessID: "roots", Protocol: rpc.Protocol{Major: 1},
			})
			return mcp.Opened{Deps: deps, Root: dir}, err
		},
	}

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	errc := make(chan error, 1)
	go func() { errc <- mcp.Serve(serverIn, serverOut, opts) }()

	path := filepath.ToSlash(rooted)
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	reader := bufio.NewReader(clientIn)
	fmt.Fprintf(clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{}},"clientInfo":{"name":"roots"}}}`+"\n")
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	fmt.Fprintf(clientOut, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	request, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request, `"roots/list"`) {
		t.Fatalf("request = %q", request)
	}
	fmt.Fprintf(clientOut, `{"jsonrpc":"2.0","id":"recollect-roots","result":{"roots":[{"uri":%q}]}}`+"\n", uri)
	fmt.Fprintf(clientOut, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_context","arguments":{}}}`+"\n")
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	clientOut.Close()
	if err := <-errc; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if first := <-opened; first == rooted {
		t.Fatalf("first bind used the root before the roots response")
	}
	if second := <-opened; second != rooted {
		t.Fatalf("rebind used %q, want %q", second, rooted)
	}
}

func TestPreInitializeRejected(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, []string{
		frame(1, "tools/call", `{"name":"memory_context","arguments":{}}`),
		frame(2, "ping", `{}`),
	})
	if len(replies) != 2 {
		t.Fatalf("replies = %d, want 2", len(replies))
	}
	if replies[0].Error == nil || replies[0].Error.Code != -32600 {
		t.Fatalf("pre-initialize tool = %+v", replies[0].Error)
	}
	if replies[1].Error != nil {
		t.Fatalf("pre-initialize ping = %+v", replies[1].Error)
	}
}

func TestDoubleInitialize(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, []string{
		frame(1, "initialize", `{"protocolVersion":"2024-11-05"}`),
		frame(2, "initialize", `{"protocolVersion":"2024-11-05"}`),
	})
	if len(replies) != 2 {
		t.Fatalf("replies = %d, want 2", len(replies))
	}
	if replies[1].Error == nil || replies[1].Error.Code != -32600 {
		t.Fatalf("second initialize = %+v", replies[1].Error)
	}
}

func TestProjectFixedSkipsRoots(t *testing.T) {
	hint := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hint, "user"), 0o700); err != nil {
		t.Fatal(err)
	}
	opened := make(chan string, 1)
	opts := mcp.Options{
		ProjectHint: hint, ProjectFixed: true, Harness: "fixed", Version: "test",
		Open: func(dir string) (mcp.Opened, error) {
			opened <- dir
			deps, err := rpc.OpenBindings(rpc.Bindings{
				ProjectDir: dir, ProjectRoot: dir, UserDir: filepath.Join(dir, "user"),
				HarnessID: "fixed", Protocol: rpc.Protocol{Major: 1},
			})
			return mcp.Opened{Deps: deps, Root: dir}, err
		},
	}
	replies := runWith(t, opts, []string{
		frame(1, "initialize", `{"protocolVersion":"2024-11-05","capabilities":{"roots":{}}}`),
	})
	if len(replies) != 1 || replies[0].Error != nil {
		t.Fatalf("initialize = %+v", replies[0].Error)
	}
	if got := <-opened; got != hint {
		t.Fatalf("opened %q, want hint %q", got, hint)
	}
}

func TestSaveRedactsCredentials(t *testing.T) {
	deps := openTestDeps(t)
	replies := runFrames(t, deps, withInit(
		frame(1, "tools/call", `{"name":"memory_save","arguments":{"description":"API note","body":"api key: AKIAIOSFODNN7EXAMPLE"}}`),
	))
	var saved struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(toolText(t, replies[1])), &saved); err != nil {
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
