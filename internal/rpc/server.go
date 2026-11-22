package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/jobs"
	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/render"
)

// Renderer builds the memory section for one context snapshot.
type Renderer func(render.Config) string

// Deps carries the bound engine dependencies. Serve builds them from the
// initialize bindings when they are not supplied.
type Deps struct {
	Store  *memory.Store
	Engine *engine.Engine
	Jobs   *jobs.Queue
	Render Renderer

	Open func(Bindings) (Deps, error)
}

// Bindings are fixed by initialize. Later requests cannot change them.
type Bindings struct {
	ProjectDir   string
	ProjectRoot  string
	UserDir      string
	HarnessID    string
	Capabilities []string
	Protocol     Protocol
}

func canonicalDir(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return absolute, nil
	}
	return resolved, nil
}

func projectRootFor(b Bindings) string {
	if strings.TrimSpace(b.ProjectRoot) == "" {
		return b.ProjectDir
	}
	return b.ProjectRoot
}

// OpenBindings constructs bound dependencies from initialize values.
func OpenBindings(b Bindings) (Deps, error) {
	return OpenBindingsWithSearch(b, "", nil)
}

// The search backend is process configuration, not a client-controlled binding.
func OpenBindingsWithSearch(b Bindings, searchBackend string, embedder memory.EmbeddingProvider) (Deps, error) {
	return OpenBindingsWithGenerator(b, searchBackend, embedder, nil)
}

// OpenBindingsWithGenerator constructs bound dependencies with a host
// generator. A nil generator reports generation as unavailable.
func OpenBindingsWithGenerator(b Bindings, searchBackend string, embedder memory.EmbeddingProvider, generator jobs.Generator) (Deps, error) {
	projectRoot := b.ProjectRoot
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = b.ProjectDir
	}
	projectDir, err := canonicalDir(b.ProjectDir)
	if err != nil {
		return Deps{}, err
	}
	userDir, err := canonicalDir(b.UserDir)
	if err != nil {
		return Deps{}, err
	}
	store := memory.NewStore(memory.Options{
		ProjectDir:    projectDir,
		UserDir:       userDir,
		Meta:          memory.ProjectMetaFor(projectRoot),
		SearchBackend: searchBackend,
		Embedder:      embedder,
	})
	eng := engine.Open(engine.Config{
		Store:      store,
		ProjectDir: projectDir,
		UserDir:    userDir,
	})
	if generator == nil {
		generator = func(context.Context, string) (string, error) { return "", jobs.ErrGenerationMissing }
	}
	queue := jobs.Open(store, filepath.Join(projectDir, ".engine"), generator)
	queue.SetHarness(b.HarnessID)
	return Deps{Store: store, Engine: eng, Jobs: queue, Render: render.MemorySection}, nil
}

type server struct {
	deps            Deps
	bindings        Bindings
	initialized     bool
	closed          bool
	generationState string
	lastErrorCode   string
	snapshotID      string
	snapshotText    string
	out             io.Writer
	enc             *json.Encoder
}

const maxFrameBytes = 16 << 20

// Serve reads JSON-RPC frames from in and writes replies to out. Diagnostics
// are the caller's responsibility and never reach out.
func Serve(in io.Reader, out io.Writer, deps Deps) error {
	s := &server{deps: deps, out: out, enc: json.NewEncoder(out)}
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
		return s.reply(req.ID, nil, failure(CodeInvalidRequest, "invalid request frame"))
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		return s.reply(req.ID, nil, failure(CodeInvalidRequest, "invalid request"))
	}
	if req.Method == "initialize" {
		return s.initialize(req)
	}
	if s.closed {
		return s.reply(req.ID, nil, failure(CodeInvalidRequest, "engine closed"))
	}
	if !s.initialized {
		return s.reply(req.ID, nil, failure(CodeInvalidRequest, "initialize required"))
	}
	switch req.Method {
	case "memory.execute":
		return s.execute(req)
	case "context.get":
		return s.contextGet(req)
	case "events.ingest":
		return s.ingest(req)
	case "jobs.next":
		return s.jobsNext(req)
	case "jobs.complete":
		return s.jobsComplete(req)
	case "status.get":
		return s.statusGet(req)
	case "maintenance.execute":
		return s.maintenanceExecute(req)
	case "engine.close":
		return s.engineClose(req)
	default:
		return s.reply(req.ID, nil, failure(CodeUnknownMethod, "unknown method: "+req.Method))
	}
}

func (s *server) reply(id json.RawMessage, result any, err *Error) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := Response{JSONRPC: jsonRPCVersion, ID: id, Error: err}
	if err != nil {
		// lastErrorCode is the latest error message. Domain errors use stable,
		// path-free identifiers the adapter displays; an unknown method echoes
		// the caller's method text.
		s.lastErrorCode = err.Message
	}
	if err == nil {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		resp.Result = data
	}
	if err := s.enc.Encode(resp); err != nil {
		return err
	}
	if flusher, ok := s.out.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

type initParams struct {
	ProjectDir      string    `json:"projectDir"`
	ProjectRoot     string    `json:"projectRoot"`
	UserDir         string    `json:"userDir"`
	HarnessID       string    `json:"harnessID"`
	Capabilities    []string  `json:"capabilities"`
	GenerationState string    `json:"generationState"`
	Protocol        *Protocol `json:"protocol"`
}

type initResult struct {
	JSONRPCVersion  string   `json:"jsonrpcVersion"`
	Protocol        Protocol `json:"protocol"`
	ProjectDir      string   `json:"projectDir"`
	UserDir         string   `json:"userDir"`
	HarnessID       string   `json:"harnessID"`
	Capabilities    []string `json:"capabilities"`
	GenerationState string   `json:"generationState"`
	Note            string   `json:"note,omitempty"`
}

func (s *server) initialize(req Request) error {
	if s.initialized {
		return s.reply(req.ID, nil, failure(CodeInvalidRequest, "already initialized"))
	}
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.reply(req.ID, nil, invalidParams("invalid initialize params"))
	}
	protocol := p.Protocol
	if protocol == nil {
		protocol = &Protocol{Major: protocolMajor}
	}
	if protocol.Major != protocolMajor {
		return s.reply(req.ID, nil, failure(CodeProtocol, "protocol major 1 required"))
	}
	if strings.TrimSpace(p.ProjectDir) == "" || strings.TrimSpace(p.UserDir) == "" {
		return s.reply(req.ID, nil, invalidParams("projectDir and userDir are required"))
	}

	generationState := p.GenerationState
	switch generationState {
	case "enabled", "disabled", "unavailable":
	default:
		generationState = "unknown"
	}
	capabilities := make([]string, 0, len(p.Capabilities))
	for _, capability := range p.Capabilities {
		if capability == "generation" && generationState != "enabled" {
			continue
		}
		capabilities = append(capabilities, capability)
	}
	b := Bindings{
		ProjectDir:   p.ProjectDir,
		ProjectRoot:  p.ProjectRoot,
		UserDir:      p.UserDir,
		HarnessID:    p.HarnessID,
		Capabilities: capabilities,
		Protocol:     *protocol,
	}
	deps := s.deps
	if deps.Store == nil {
		opener := deps.Open
		if opener == nil {
			opener = OpenBindings
		}
		var err error
		deps, err = opener(b)
		if err != nil {
			return s.reply(req.ID, nil, storageFailure(err))
		}
	}
	s.deps = deps
	s.bindings = b
	s.generationState = generationState
	s.initialized = true

	result := initResult{
		JSONRPCVersion:  jsonRPCVersion,
		Protocol:        *protocol,
		ProjectDir:      p.ProjectDir,
		UserDir:         p.UserDir,
		HarnessID:       p.HarnessID,
		Capabilities:    capabilities,
		GenerationState: generationState,
	}
	if protocol.Minor != 0 {
		result.Note = "minor version difference reported"
	}
	return s.reply(req.ID, result, nil)
}

// newLease returns an opaque lease token for one jobs.next grant.
func newLease() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func (s *server) invalid(id json.RawMessage, message string) error {
	return s.reply(id, nil, invalidParams(message))
}

func (s *server) wrap(id json.RawMessage, err error, mapErr func(error) *Error) error {
	if err == nil {
		return nil
	}
	return s.reply(id, nil, mapErr(err))
}
