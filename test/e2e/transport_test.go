package e2e

// audit: behavior - Stage 5a binary transport: initialize, memory ops, revisions, isolation, jobs, protocol, concurrency, close.
// audit: regression - existing engine, jobs, render, and memory contracts stay unchanged.
// audit: coverage gap - real built binary over stdin/stdout; no in-process substitution.
// audit: seam - commands cross the process boundary with newline-delimited JSON-RPC frames.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "cannot locate test source")
		os.Exit(1)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	dir, err := os.MkdirTemp("", "recollect-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binaryPath = filepath.Join(dir, "recollect")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/recollect")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type client struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr bytes.Buffer
	nextID int
	mu     sync.Mutex
}

func start(t *testing.T) *client {
	t.Helper()
	cmd := exec.Command(binaryPath)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &client{cmd: cmd, in: in, out: bufio.NewReader(out)}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *client) callRaw(method string, params any) (response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	raw := []byte("{}")
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		if err != nil {
			return response{}, err
		}
	}
	frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`+"\n", c.nextID, method, raw)
	if _, err := io.WriteString(c.in, frame); err != nil {
		return response{}, fmt.Errorf("write frame: %w", err)
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		return response{}, fmt.Errorf("read frame (%s): %w; stderr: %s", method, err, c.stderr.String())
	}
	var resp response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return response{}, fmt.Errorf("bad frame %q: %w", line, err)
	}
	return resp, nil
}

func (c *client) call(t *testing.T, method string, params any) response {
	t.Helper()
	resp, err := c.callRaw(method, params)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func ok(t *testing.T, resp response) json.RawMessage {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error %d %s", resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Result) == 0 {
		t.Fatal("empty result")
	}
	return resp.Result
}

func wantCode(t *testing.T, resp response, code int) {
	t.Helper()
	if resp.Error == nil {
		t.Fatalf("want error %d, got success %s", code, resp.Result)
	}
	if resp.Error.Code != code {
		t.Fatalf("want error %d, got %d %s", code, resp.Error.Code, resp.Error.Message)
	}
}

type initParams struct {
	ProjectDir   string         `json:"projectDir"`
	UserDir      string         `json:"userDir"`
	HarnessID    string         `json:"harnessID"`
	Capabilities []string       `json:"capabilities"`
	Protocol     map[string]int `json:"protocol"`
}

type execParams struct {
	Target      string `json:"target"`
	Operation   string `json:"operation"`
	RequestID   string `json:"requestID,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Revision    string `json:"revision,omitempty"`
	Query       string `json:"query,omitempty"`
	Scene       string `json:"scene,omitempty"`
}

type factView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type execResult struct {
	Name     string     `json:"name"`
	Revision string     `json:"revision"`
	Fact     *factView  `json:"fact"`
	Matches  []factView `json:"matches"`
}

type jobView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	HarnessID string `json:"harnessID"`
	State     string `json:"state"`
	Prompt    string `json:"prompt"`
}

type dirs struct {
	project string
	user    string
}

func tempDirs(t *testing.T) dirs {
	t.Helper()
	base := t.TempDir()
	return dirs{project: filepath.Join(base, "project"), user: filepath.Join(base, "user")}
}

func initialize(t *testing.T, c *client, d dirs, harness string, protocol map[string]int) {
	t.Helper()
	resp := c.call(t, "initialize", initParams{
		ProjectDir: d.project, UserDir: d.user, HarnessID: harness,
		Capabilities: []string{"memory"}, Protocol: protocol,
	})
	ok(t, resp)
}

func appendFact(t *testing.T, c *client, id, desc, body string) execResult {
	t.Helper()
	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "append", RequestID: id,
		Description: desc, Body: body,
	}))
	var out execResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInitializeAppendReadSearch(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-a", map[string]int{"major": 1})

	added := appendFact(t, c, "add-1", "deploy port", "The deploy port is 8443.")
	if added.Name == "" || added.Revision == "" {
		t.Fatalf("append returned no name or revision: %+v", added)
	}

	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "read", Name: added.Name,
	}))
	var read execResult
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if read.Fact == nil || read.Fact.Name != added.Name {
		t.Fatalf("read did not return the fact: %s", raw)
	}
	if !strings.Contains(read.Fact.Body, "8443") {
		t.Fatalf("read body lost content: %s", read.Fact.Body)
	}
	if read.Revision != added.Revision {
		t.Fatalf("read revision %q differs from append %q", read.Revision, added.Revision)
	}

	raw = ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "search", Query: "deploy port",
	}))
	var found struct {
		Matches []factView `json:"matches"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Matches) == 0 || found.Matches[0].Name != added.Name {
		t.Fatalf("search did not find the fact: %s", raw)
	}
}

func TestCorrectionRevisionGuards(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-a", map[string]int{"major": 1})
	added := appendFact(t, c, "add-1", "cache ttl", "The cache ttl is 10 minutes.")

	wantCode(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "correct", RequestID: "fix-0",
		Name: added.Name, Description: "cache ttl", Body: "The cache ttl is 20 minutes.",
	}), -32002)

	raw := ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "read", Name: added.Name,
	}))
	var read execResult
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}

	raw = ok(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "correct", RequestID: "fix-1",
		Name: added.Name, Revision: read.Revision,
		Description: "cache ttl", Body: "The cache ttl is 20 minutes.",
	}))
	var fixed execResult
	if err := json.Unmarshal(raw, &fixed); err != nil {
		t.Fatal(err)
	}
	if fixed.Revision == read.Revision || fixed.Revision == "" {
		t.Fatalf("correction did not advance revision: %s", raw)
	}

	wantCode(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "correct", RequestID: "fix-2",
		Name: added.Name, Revision: read.Revision,
		Description: "cache ttl", Body: "The cache ttl is 30 minutes.",
	}), -32003)
}

func TestProjectIsolation(t *testing.T) {
	a, b := tempDirs(t), tempDirs(t)
	ca, cb := start(t), start(t)
	defer ca.cmd.Process.Kill()
	defer cb.cmd.Process.Kill()
	initialize(t, ca, a, "harness-a", map[string]int{"major": 1})
	initialize(t, cb, b, "harness-b", map[string]int{"major": 1})
	added := appendFact(t, ca, "add-1", "secret marker", "marker value 77123.")

	wantCode(t, cb.call(t, "memory.execute", execParams{
		Target: "project", Operation: "read", Name: added.Name,
	}), -32602)
}

type record struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionID"`
	Generation string `json:"generation"`
	Time       string `json:"time"`
	Role       string `json:"role"`
	Text       string `json:"text"`
}

type boundary struct {
	SessionID  string `json:"sessionID"`
	Generation string `json:"generation"`
	Kind       string `json:"kind"`
	Time       string `json:"time"`
	Available  bool   `json:"available"`
}

func TestEventsJobsAndIdempotentCompletion(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})

	resp := c.call(t, "events.ingest", map[string]any{
		"records":    []record{{ID: "r1", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "interrupted", Time: "2026-11-18T00:00:01Z", Available: true}},
	})
	ok(t, resp)

	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": false}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatal(err)
	}
	if leased.Job == nil || leased.Lease == "" {
		t.Fatalf("no job leased: %s", raw)
	}
	if leased.Job.HarnessID != "bound-harness" {
		t.Fatalf("job leased with wrong harness: %s", raw)
	}

	for i := 0; i < 2; i++ {
		ok(t, c.call(t, "jobs.complete", map[string]any{
			"jobID": leased.Job.ID, "lease": leased.Lease,
			"output": "A compacted summary.",
		}))
	}
}

// audit: behavior - a model job leases through jobs.next for host generation.
// audit: regression - nil local generator would leave every model job pending.
// audit: coverage gap - the existing jobs test used a deterministic fallback.
// audit: seam - the test drives events.ingest and jobs.next through the binary.
func TestModelJobLeasesForHostGeneration(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	ok(t, c.call(t, "events.ingest", map[string]any{
		"records":    []record{{ID: "r1", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete", Time: "2026-11-18T00:00:01Z", Available: true}},
	}))
	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": true}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatal(err)
	}
	if leased.Job == nil || leased.Job.Kind != "fact" {
		t.Fatalf("model job not leased: %s", raw)
	}
	if leased.Job.Prompt == "" {
		t.Fatalf("leased model job has no engine-owned prompt: %s", raw)
	}
}

// audit: behavior - a timed-out job stays pending in status and close reporting.
// audit: regression - retry-wait work must not disappear while its cooldown runs.
// audit: coverage gap - the direct queue test does not cross the RPC boundary.
// audit: seam - the test drives events.ingest, jobs.complete, status.get, and engine.close.
func TestRetryWaitJobCountsAsPending(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	ok(t, c.call(t, "events.ingest", map[string]any{
		"records":    []record{{ID: "r1", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete", Time: "2026-11-18T00:00:01Z", Available: true}},
	}))
	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": true}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil || leased.Job == nil {
		t.Fatalf("lease: %v %s", err, raw)
	}
	ok(t, c.call(t, "jobs.complete", map[string]any{
		"jobID": leased.Job.ID, "lease": leased.Lease, "failure": "generation_timeout",
	}))

	statusRaw := ok(t, c.call(t, "status.get", map[string]any{}))
	var status struct {
		Pending   int `json:"pending"`
		Conflicts int `json:"conflicts"`
	}
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Pending < 1 || status.Conflicts != 0 {
		t.Fatalf("status after timeout = %s", statusRaw)
	}

	closeRaw := ok(t, c.call(t, "engine.close", map[string]any{"deadlineMS": 1000}))
	var closed struct {
		Pending []jobView `json:"pending"`
	}
	if err := json.Unmarshal(closeRaw, &closed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range closed.Pending {
		if job.ID == leased.Job.ID && job.State == "retry_wait" {
			found = true
		}
	}
	if !found {
		t.Fatalf("retry-wait job missing from close report: %s", closeRaw)
	}
}

// audit: behavior - a host without generation capability does not lease model work.
// audit: regression - such a host would receive a fact job it cannot execute.
// audit: coverage gap - the generation flag was accepted but never enforced.
// audit: seam - the test drives events.ingest and jobs.next through the binary.
func TestGenerationCapabilityGatesModelJobs(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	ok(t, c.call(t, "events.ingest", map[string]any{
		"records":    []record{{ID: "r1", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete", Time: "2026-11-18T00:00:01Z", Available: true}},
	}))
	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": false}))
	var leased struct {
		Job *jobView `json:"job"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatal(err)
	}
	if leased.Job != nil && leased.Job.Kind == "fact" {
		t.Fatalf("model job leased without generation capability: %s", raw)
	}
}

// audit: behavior - maintenance.execute reports status through the binary.
// audit: regression - the method listed in the spec would be unknown.
// audit: coverage gap - no test called maintenance.execute.
// audit: seam - the test calls the public JSON-RPC method after initialize.
func TestMaintenanceStatus(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	raw := ok(t, c.call(t, "maintenance.execute", map[string]any{"action": "status"}))
	var status struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Action != "status" {
		t.Fatalf("maintenance status = %s", raw)
	}
}

// audit: behavior - a write does not change the cached context snapshot.
// audit: regression - append would silently replace the session prompt block.
// audit: coverage gap - existing context tests never combined writes and snapshots.
// audit: seam - the test drives context.get and memory.execute through the binary.
func TestWriteDoesNotChangeSnapshot(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	before := ok(t, c.call(t, "context.get", map[string]any{"refresh": false}))
	var first struct {
		SnapshotID string `json:"snapshotID"`
	}
	if err := json.Unmarshal(before, &first); err != nil {
		t.Fatal(err)
	}
	ok(t, c.call(t, "memory.execute", map[string]any{
		"target": "project", "operation": "append", "requestID": "snap-1",
		"description": "snapshot fact", "body": "snapshot body",
	}))
	after := ok(t, c.call(t, "context.get", map[string]any{"refresh": false}))
	var second struct {
		SnapshotID string `json:"snapshotID"`
	}
	if err := json.Unmarshal(after, &second); err != nil {
		t.Fatal(err)
	}
	if second.SnapshotID != first.SnapshotID {
		t.Fatalf("snapshot changed after write: %s -> %s", first.SnapshotID, second.SnapshotID)
	}
}

func TestProtocolMajorRejectedBeforeWrite(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	wantCode(t, c.call(t, "initialize", initParams{
		ProjectDir: d.project, UserDir: d.user, HarnessID: "harness-a",
		Protocol: map[string]int{"major": 2},
	}), -32001)
	for _, path := range []string{filepath.Join(d.project, ".engine"), filepath.Join(d.user, ".engine")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("protocol mismatch wrote state at %s", path)
		}
	}
}

func TestConcurrentAppendsSurvive(t *testing.T) {
	d := tempDirs(t)
	ca, cb := start(t), start(t)
	defer ca.cmd.Process.Kill()
	defer cb.cmd.Process.Kill()
	initialize(t, ca, d, "harness-a", map[string]int{"major": 1})
	initialize(t, cb, d, "harness-b", map[string]int{"major": 1})

	appendAsync := func(c *client, id, desc, body string) <-chan error {
		out := make(chan error, 1)
		go func() {
			resp, err := c.callRaw("memory.execute", execParams{
				Target: "project", Operation: "append", RequestID: id,
				Description: desc, Body: body,
			})
			if err != nil {
				out <- err
				return
			}
			if resp.Error != nil {
				out <- fmt.Errorf("%d %s", resp.Error.Code, resp.Error.Message)
				return
			}
			out <- nil
		}()
		return out
	}
	aErr := appendAsync(ca, "parallel-1", "alpha marker", "alpha value 4242.")
	bErr := appendAsync(cb, "parallel-2", "beta marker", "beta value 8686.")
	if err := <-aErr; err != nil {
		t.Fatal(err)
	}
	if err := <-bErr; err != nil {
		t.Fatal(err)
	}

	cc := start(t)
	defer cc.cmd.Process.Kill()
	initialize(t, cc, d, "harness-c", map[string]int{"major": 1})
	raw := ok(t, cc.call(t, "memory.execute", execParams{
		Target: "project", Operation: "search", Query: "marker",
	}))
	var found struct {
		Matches []factView `json:"matches"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range found.Matches {
		names[m.Name] = true
	}
	if len(names) < 2 {
		t.Fatalf("concurrent appends did not both survive: %s", raw)
	}
}

func TestEngineCloseReturnsDurableState(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-a", map[string]int{"major": 1})
	added := appendFact(t, c, "add-1", "durable marker", "durable value 9001.")

	raw := ok(t, c.call(t, "engine.close", map[string]any{"deadlineMS": 1000}))
	var closed struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(raw, &closed); err != nil {
		t.Fatal(err)
	}

	next := start(t)
	defer next.cmd.Process.Kill()
	initialize(t, next, d, "harness-b", map[string]int{"major": 1})
	read := ok(t, next.call(t, "memory.execute", execParams{
		Target: "project", Operation: "read", Name: added.Name,
	}))
	var view execResult
	if err := json.Unmarshal(read, &view); err != nil {
		t.Fatal(err)
	}
	if view.Fact == nil || !strings.Contains(view.Fact.Body, "9001") {
		t.Fatalf("later process could not recover durable fact: %s", read)
	}
}
