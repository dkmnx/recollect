// Package mcp serves Recollect over the Model Context Protocol stdio
// transport.
package mcp

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/render"
	"github.com/dkmnx/recollect/internal/rpc"
)

const (
	maxFrameBytes   = 16 << 20
	maxResultBytes  = 64 << 10
	newestProtocol  = "2024-11-05"
	jsonRPCVersion  = "2.0"
	codeParseError  = -32700
	codeInvalidReq  = -32600
	codeUnknownTool = -32601
	codeInvalidArgs = -32602
	codeInternal    = -32603
)

// Request is one newline-delimited JSON-RPC frame.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Response is one JSON-RPC reply.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC failure.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct {
	deps    rpc.Deps
	root    string
	harness string
	version string
	enc     *json.Encoder
}

// Serve reads MCP JSON-RPC frames from in and writes replies to out.
func Serve(in io.Reader, out io.Writer, deps rpc.Deps, root, harness, version string) error {
	s := &server{deps: deps, root: root, harness: harness, version: version, enc: json.NewEncoder(out)}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := s.handle(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *server) handle(line []byte) error {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return s.reply(nil, nil, &Error{Code: codeParseError, Message: "parse error"})
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		return s.reply(req.ID, nil, &Error{Code: codeInvalidReq, Message: "invalid request"})
	}
	// JSON-RPC notifications carry no id and must never receive a reply.
	if len(req.ID) == 0 {
		return nil
	}
	switch req.Method {
	case "initialize":
		return s.initialize(req)
	case "notifications/initialized":
		return nil
	case "ping":
		return s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		return s.reply(req.ID, map[string]any{"tools": toolList()}, nil)
	case "tools/call":
		return s.callTool(req)
	case "resources/list":
		return s.reply(req.ID, map[string]any{"resources": resources()}, nil)
	case "resources/read":
		return s.readResource(req)
	default:
		return s.reply(req.ID, nil, &Error{Code: codeUnknownTool, Message: "unknown method: " + req.Method})
	}
}

func (s *server) reply(id json.RawMessage, result any, err *Error) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return s.enc.Encode(Response{JSONRPC: jsonRPCVersion, ID: id, Result: result, Error: err})
}

type initParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name string `json:"name"`
	} `json:"clientInfo"`
}

func (s *server) initialize(req Request) error {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.reply(req.ID, nil, &Error{Code: codeInvalidReq, Message: "invalid initialize params"})
	}
	if s.harness == "" && strings.TrimSpace(p.ClientInfo.Name) != "" {
		s.harness = p.ClientInfo.Name
	}
	version := newestProtocol
	if strings.TrimSpace(p.ProtocolVersion) == newestProtocol {
		version = p.ProtocolVersion
	}
	return s.reply(req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"serverInfo": map[string]any{"name": "recollect", "version": s.version},
	}, nil)
}

func toolList() []map[string]any {
	return []map[string]any{
		tool("memory_search", "Search project memory.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string"},
				"scene":  map[string]any{"type": "string"},
				"limit":  map[string]any{"type": "integer"},
				"target": map[string]any{"type": "string", "enum": []string{"project", "user"}},
			},
			"required": []string{"query"},
		}, map[string]any{"readOnlyHint": true}),
		tool("memory_read", "Read one fact with its revision.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string"},
				"target": map[string]any{"type": "string", "enum": []string{"project", "user"}},
			},
			"required": []string{"name"},
		}, map[string]any{"readOnlyHint": true}),
		tool("memory_save", "Save a fact to memory.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"body":        map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
				"target":      map[string]any{"type": "string", "enum": []string{"project", "user"}},
			},
			"required": []string{"description"},
		}, map[string]any{"destructiveHint": false, "idempotentHint": false}),
		tool("memory_correct", "Correct a fact after reading it.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"body":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"revision":    map[string]any{"type": "string"},
				"target":      map[string]any{"type": "string", "enum": []string{"project", "user"}},
			},
			"required": []string{"name", "revision"},
		}, map[string]any{"destructiveHint": true}),
		tool("memory_delete", "Delete a fact after reading it.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":     map[string]any{"type": "string"},
				"revision": map[string]any{"type": "string"},
				"target":   map[string]any{"type": "string", "enum": []string{"project", "user"}},
			},
			"required": []string{"name", "revision"},
		}, map[string]any{"destructiveHint": true}),
		tool("memory_context", "Read the rendered memory context.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}, map[string]any{"readOnlyHint": true}),
	}
}

func tool(name, description string, schema, annotations map[string]any) map[string]any {
	return map[string]any{
		"name": name, "description": description,
		"inputSchema": schema, "annotations": annotations,
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *server) callTool(req Request) error {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.reply(req.ID, nil, &Error{Code: codeInvalidArgs, Message: "invalid tool params"})
	}
	text, err := s.runTool(p.Name, p.Arguments)
	if err != nil {
		return s.reply(req.ID, toolResult(err.Error(), true), nil)
	}
	return s.reply(req.ID, toolResult(text, false), nil)
}

func toolResult(text string, isError bool) map[string]any {
	head, truncated := memory.HeadBytes(text, maxResultBytes)
	if truncated {
		head += "\n[truncated]"
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": head}},
		"isError": isError,
	}
}

func (s *server) runTool(name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	switch name {
	case "memory_search":
		return s.search(raw)
	case "memory_read":
		return s.read(raw)
	case "memory_save":
		return s.save(raw)
	case "memory_correct":
		return s.correct(raw)
	case "memory_delete":
		return s.deleteFact(raw)
	case "memory_context":
		return filterText(render.MemorySection(render.Config{Enabled: true, Store: s.deps.Store, Root: s.root})), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func scopeFor(target string) (engine.Scope, error) {
	switch target {
	case "", "project":
		return engine.ScopeProject, nil
	case "user":
		return engine.ScopeUser, nil
	default:
		return engine.ScopeProject, fmt.Errorf("target must be project or user, got %q", target)
	}
}

func (s *server) search(raw json.RawMessage) (string, error) {
	var p struct {
		Query  string `json:"query"`
		Scene  string `json:"scene"`
		Limit  int    `json:"limit"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return "", errors.New("query is required")
	}
	scope, err := scopeFor(p.Target)
	if err != nil {
		return "", err
	}
	if scope == engine.ScopeUser {
		matches, err := s.deps.Store.SearchUser(p.Query)
		if err != nil {
			return "", errors.New("search failed")
		}
		for i := range matches {
			matches[i].Text = filterText(matches[i].Text)
			for j := range matches[i].Context {
				matches[i].Context[j] = filterText(matches[i].Context[j])
			}
		}
		return marshal(matches)
	}
	matches, err := s.deps.Store.SearchFacts(p.Query, p.Scene, p.Limit)
	if err != nil {
		return "", errors.New("search failed")
	}
	out := make([]map[string]string, len(matches))
	for i, match := range matches {
		out[i] = map[string]string{
			"name": filterText(match.Name), "description": filterText(match.Description),
			"body": filterText(match.Body), "field": match.Field, "kind": match.Kind,
		}
	}
	return marshal(out)
}

func (s *server) read(raw json.RawMessage) (string, error) {
	var p struct {
		Name   string `json:"name"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", errors.New("invalid arguments")
	}
	scope, err := scopeFor(p.Target)
	if err != nil {
		return "", err
	}
	if scope == engine.ScopeProject && strings.TrimSpace(p.Name) == "" {
		return "", errors.New("name is required for project reads")
	}
	got, err := s.deps.Engine.Read(scope, p.Name)
	if err != nil {
		return "", guidance(err, p.Name)
	}
	out := map[string]any{
		"name": filterText(got.Name), "revision": string(got.Revision),
		"content": filterText(got.Content),
	}
	if got.Fact != nil {
		out["fact"] = map[string]string{
			"name": filterText(got.Fact.Name), "description": filterText(got.Fact.Description),
			"body": filterText(got.Fact.Body),
		}
	}
	return marshal(out)
}

func (s *server) save(raw json.RawMessage) (string, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Reason      string `json:"reason"`
		Target      string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", errors.New("invalid arguments")
	}
	scope, err := scopeFor(p.Target)
	if err != nil {
		return "", err
	}
	id, err := newRequestID()
	if err != nil {
		return "", err
	}
	res, err := s.deps.Engine.AppendFact(engine.AppendRequest{
		RequestID: id, Name: filterText(p.Name), Description: filterText(p.Description),
		Body: filterText(p.Body), Reason: filterText(p.Reason), Scope: scope,
	})
	if err != nil {
		return "", guidance(err, p.Name)
	}
	return marshal(map[string]string{"name": res.Name, "revision": string(res.Revision)})
}

func (s *server) correct(raw json.RawMessage) (string, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Revision    string `json:"revision"`
		Target      string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Revision == "" {
		return "", errors.New("revision is required")
	}
	scope, err := scopeFor(p.Target)
	if err != nil {
		return "", err
	}
	if scope == engine.ScopeUser && strings.TrimSpace(p.Body) == "" {
		return "", errors.New("user correction requires a nonblank body because it replaces all of user.md")
	}
	if scope == engine.ScopeProject && strings.TrimSpace(p.Name) == "" {
		return "", errors.New("name is required for project corrections")
	}
	id, err := newRequestID()
	if err != nil {
		return "", err
	}
	res, err := s.deps.Engine.CorrectFact(engine.CorrectRequest{
		RequestID: id, Name: filterText(p.Name), Expected: engine.Revision(p.Revision),
		Description: filterText(p.Description), Body: filterText(p.Body), Scope: scope,
	})
	if err != nil {
		return "", guidance(err, p.Name)
	}
	return marshal(map[string]string{"name": res.Name, "revision": string(res.Revision)})
}

func (s *server) deleteFact(raw json.RawMessage) (string, error) {
	var p struct {
		Name     string `json:"name"`
		Revision string `json:"revision"`
		Target   string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Revision == "" {
		return "", errors.New("revision is required")
	}
	scope, err := scopeFor(p.Target)
	if err != nil {
		return "", err
	}
	if scope == engine.ScopeProject && strings.TrimSpace(p.Name) == "" {
		return "", errors.New("name is required for project deletions")
	}
	id, err := newRequestID()
	if err != nil {
		return "", err
	}
	if err := s.deps.Engine.DeleteFact(engine.DeleteRequest{
		RequestID: id, Name: p.Name, Expected: engine.Revision(p.Revision), Scope: scope,
	}); err != nil {
		return "", guidance(err, p.Name)
	}
	return marshal(map[string]string{"name": p.Name})
}

func guidance(err error, name string) error {
	switch {
	case errors.Is(err, engine.ErrReadRequired):
		return fmt.Errorf("call memory_read for %q first, then pass its revision", name)
	case errors.Is(err, engine.ErrRevisionConflict):
		return errors.New("the revision is stale; call memory_read again and retry with the new revision")
	case errors.Is(err, engine.ErrNotFound), memory.CodeOf(err) == memory.CodeFactNotFound:
		return fmt.Errorf("fact %q was not found", name)
	case errors.Is(err, engine.ErrReasonRequired):
		return errors.New("reason is required for user memory writes")
	case errors.Is(err, engine.ErrBusy):
		return errors.New("memory is busy; retry the operation")
	case errors.Is(err, engine.ErrConflict):
		return errors.New("memory conflict; read the current state and retry")
	default:
		return errors.New("memory operation failed")
	}
}

func filterText(text string) string {
	return memory.Redact(memory.StripPrivateContent(text))
}

func marshal(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func newRequestID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf[:])
	return hex.EncodeToString(sum[:8]), nil
}

func resources() []map[string]any {
	return []map[string]any{
		{"uri": "recollect://context", "name": "Recollect context", "mimeType": "text/markdown"},
		{"uri": "recollect://status", "name": "Recollect status", "mimeType": "application/json"},
	}
}

func (s *server) readResource(req Request) error {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.reply(req.ID, nil, &Error{Code: codeInvalidArgs, Message: "invalid resource params"})
	}
	var text, mime string
	switch p.URI {
	case "recollect://context":
		text = filterText(render.MemorySection(render.Config{Enabled: true, Store: s.deps.Store, Root: s.root}))
		mime = "text/markdown"
	case "recollect://status":
		facts, err := s.deps.Store.ListFacts()
		if err != nil {
			return s.reply(req.ID, nil, &Error{Code: codeInternal, Message: "status unavailable"})
		}
		all, err := s.deps.Jobs.Status()
		if err != nil {
			return s.reply(req.ID, nil, &Error{Code: codeInternal, Message: "status unavailable"})
		}
		text, err = marshal(map[string]any{"facts": len(facts), "pending": len(all)})
		if err != nil {
			return s.reply(req.ID, nil, &Error{Code: codeInternal, Message: "status unavailable"})
		}
		mime = "application/json"
	default:
		return s.reply(req.ID, nil, &Error{Code: codeInvalidArgs, Message: "unknown resource: " + p.URI})
	}
	return s.reply(req.ID, map[string]any{
		"contents": []map[string]any{{"uri": p.URI, "mimeType": mime, "text": text}},
	}, nil)
}
