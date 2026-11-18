package engine_test

// audit: behavior - Stage 3 revision guards, project isolation, receipts, session cursors.
// audit: regression - memory package contracts stay unchanged and pass without edits.
// audit: coverage gap - revision rejection, scoped isolation, retry idempotency, cursor files.
// audit: seam - tests use public engine and memory APIs and the documented .engine layout.
import (
	"errors"
	"os"
	"path/filepath"
	"recollect/internal/engine"
	"recollect/internal/memory"
	"strings"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) (*engine.Engine, *memory.Store, string, string) {
	t.Helper()
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	userDir := filepath.Join(base, "user")
	store := memory.NewStore(memory.Options{
		ProjectDir: projectDir,
		UserDir:    userDir,
		Meta:       memory.ProjectMetaFor(projectDir),
	})
	eng := engine.Open(engine.Config{
		Store:       store,
		ProjectDir:  projectDir,
		UserDir:     userDir,
		LockTimeout: 2 * time.Second,
	})
	return eng, store, projectDir, userDir
}

func TestCorrectFactRequiresReadAndRejectsStaleRevision(t *testing.T) {
	eng, store, _, _ := newTestEngine(t)
	added, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "add-1",
		Description: "alpha fact",
		Body:        "alpha original body",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	_, err = eng.CorrectFact(engine.CorrectRequest{
		RequestID: "fix-no-revision",
		Name:      added.Name,
		Body:      "alpha changed body",
	})
	if !errors.Is(err, engine.ErrReadRequired) {
		t.Fatalf("correction without a revision: got %v, want read_required", err)
	}

	_, err = eng.CorrectFact(engine.CorrectRequest{
		RequestID: "fix-stale",
		Name:      added.Name,
		Expected:  engine.Revision("deadbeef"),
		Body:      "alpha changed body",
	})
	if !errors.Is(err, engine.ErrRevisionConflict) {
		t.Fatalf("correction with a stale revision: got %v, want revision_conflict", err)
	}

	fact, err := store.ReadFact(added.Name)
	if err != nil {
		t.Fatalf("read fact after rejected corrections: %v", err)
	}
	if fact.Body != "alpha original body" {
		t.Fatalf("body after rejected corrections = %q, want the original", fact.Body)
	}

	current, err := eng.ReadRevision(added.Name)
	if err != nil {
		t.Fatalf("read revision: %v", err)
	}
	corrected, err := eng.CorrectFact(engine.CorrectRequest{
		RequestID: "fix-current",
		Name:      added.Name,
		Expected:  current,
		Body:      "alpha changed body",
	})
	if err != nil {
		t.Fatalf("correction with the current revision: %v", err)
	}
	if corrected.Revision == current {
		t.Fatal("correction must advance the revision")
	}
	fact, err = store.ReadFact(added.Name)
	if err != nil {
		t.Fatalf("read corrected fact: %v", err)
	}
	if fact.Body != "alpha changed body" {
		t.Fatalf("corrected body = %q", fact.Body)
	}
}

func TestProjectStoresAreIsolated(t *testing.T) {
	first, firstStore, _, _ := newTestEngine(t)
	_, secondStore, _, _ := newTestEngine(t)
	if _, err := first.AppendFact(engine.AppendRequest{
		RequestID:   "isolated-1",
		Description: "zephyr only fact",
		Body:        "zephyr only body",
	}); err != nil {
		t.Fatalf("append to first project: %v", err)
	}

	matches, err := secondStore.SearchFacts("zephyr", "", 0)
	if err != nil {
		t.Fatalf("search second project: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("second project found %d isolated matches, want none", len(matches))
	}
	facts, err := secondStore.ListFacts()
	if err != nil {
		t.Fatalf("list second project: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("second project listed %d facts, want none", len(facts))
	}
	matches, err = firstStore.SearchFacts("zephyr", "", 0)
	if err != nil {
		t.Fatalf("search first project: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("owning project must find its fact")
	}
}

func TestAppendReceiptRetryReturnsTheRecordedResult(t *testing.T) {
	eng, store, _, _ := newTestEngine(t)
	req := engine.AppendRequest{
		RequestID:   "retry-1",
		Description: "retry fact",
		Body:        "retry body",
	}
	first, err := eng.AppendFact(req)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := eng.AppendFact(req)
	if err != nil {
		t.Fatalf("retried append: %v", err)
	}
	if first != second {
		t.Fatalf("retry result = %+v, want %+v", second, first)
	}
	facts, err := store.ListFacts()
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("retry produced %d facts, want one", len(facts))
	}
	if facts[0].Name != first.Name {
		t.Fatalf("committed fact = %q, want %q", facts[0].Name, first.Name)
	}
}

func TestExpiredReceiptRejectsStaleRetry(t *testing.T) {
	eng, _, projectDir, _ := newTestEngine(t)
	receipt := map[string]string{
		"requestID": "old-1",
		"scope":     "project",
		"op":        "append",
		"key":       "old fact",
		"name":      "old-fact",
		"revision":  "abc",
		"timestamp": "2020-01-01T00:00:00Z",
	}
	writeJSON(t, filepath.Join(projectDir, ".engine", "receipts", stateIDFor("old-1")+".json"), receipt)
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "old-1",
		Description: "old fact",
		Body:        "old body",
	}); !errors.Is(err, engine.ErrStaleRequest) {
		t.Fatalf("stale retry: got %v, want stale_request", err)
	}
}

func TestCorruptTombstoneIsReplacedBeforePrune(t *testing.T) {
	eng, _, projectDir, _ := newTestEngine(t)
	receipt := map[string]string{
		"requestID": "corrupt-tomb",
		"scope":     "project",
		"op":        "append",
		"key":       "corrupt fact",
		"name":      "corrupt-fact",
		"timestamp": "2020-01-01T00:00:00Z",
	}
	writeJSON(t, filepath.Join(projectDir, ".engine", "receipts", stateIDFor("corrupt-tomb")+".json"), receipt)
	tombPath := filepath.Join(projectDir, ".engine", "stale", stateIDFor("corrupt-tomb")+".json")
	writeJSON(t, tombPath, map[string]string{"broken": "yes"})
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "corrupt-tomb",
		Description: "corrupt fact",
		Body:        "corrupt body",
	}); !errors.Is(err, engine.ErrStaleRequest) {
		t.Fatalf("retry after corrupt tombstone: got %v, want stale_request", err)
	}
}

func TestExpiredTombstoneIsRemoved(t *testing.T) {
	eng, _, projectDir, _ := newTestEngine(t)
	stale := map[string]string{"requestID": "ancient", "prunedAt": "2020-01-01T00:00:00Z"}
	path := filepath.Join(projectDir, ".engine", "stale", stateIDFor("ancient")+".json")
	writeJSON(t, path, stale)
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ancient tombstone still present: %v", err)
	}
}

func TestRequestIDCannotEscapeEngineDir(t *testing.T) {
	eng, _, projectDir, _ := newTestEngine(t)
	base := filepath.Dir(projectDir)
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "../../../outside",
		Description: "traversal attempt",
		Body:        "traversal body",
	}); err != nil {
		t.Fatalf("append with traversal request ID: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "outside.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request ID escaped the engine directory: %v", err)
	}
}

func TestReceiptRejectsDifferentOperation(t *testing.T) {
	eng, _, _, _ := newTestEngine(t)
	added, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "reuse-1",
		Description: "original fact",
		Body:        "original body",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	_, err = eng.CorrectFact(engine.CorrectRequest{
		RequestID:   "reuse-1",
		Name:        "different fact",
		Expected:    engine.Revision("any"),
		Description: "different fact",
		Body:        "different body",
	})
	if !errors.Is(err, engine.ErrConflict) {
		t.Fatalf("reused request ID: got %v, want conflict", err)
	}
	if added.Name == "" {
		t.Fatal("append did not return a committed name")
	}
}

func TestUserAppendRequiresReason(t *testing.T) {
	eng, _, _, _ := newTestEngine(t)
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID: "user-no-reason",
		Scope:     engine.ScopeUser,
		Body:      "prefer tabs",
	}); err == nil {
		t.Fatal("user append without a reason must fail")
	}
}

func TestUserAppendPersistsReason(t *testing.T) {
	eng, store, _, _ := newTestEngine(t)
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID: "user-reason-1",
		Scope:     engine.ScopeUser,
		Reason:    "style preference",
		Body:      "prefer tabs",
	}); err != nil {
		t.Fatalf("user append: %v", err)
	}
	content, err := store.ReadUser()
	if err != nil {
		t.Fatalf("read user memory: %v", err)
	}
	if !strings.Contains(content, "<!-- mem-append-reason: style preference -->") {
		t.Fatalf("user reason not persisted: %q", content)
	}
}

func TestUserScopeUsesRevisionGuard(t *testing.T) {
	eng, store, _, _ := newTestEngine(t)
	if _, err := eng.AppendFact(engine.AppendRequest{
		RequestID: "user-1",
		Scope:     engine.ScopeUser,
		Reason:    "style preference",
		Body:      "prefer tabs",
	}); err != nil {
		t.Fatalf("user append: %v", err)
	}
	revision, err := eng.ReadRevisionForScope(engine.ScopeUser, "")
	if err != nil {
		t.Fatalf("read user revision: %v", err)
	}
	if _, err := eng.CorrectFact(engine.CorrectRequest{
		RequestID: "user-stale",
		Scope:     engine.ScopeUser,
		Name:      "user",
		Expected:  engine.Revision("deadbeef"),
		Body:      "prefer spaces",
	}); !errors.Is(err, engine.ErrRevisionConflict) {
		t.Fatalf("stale user correction: got %v, want revision_conflict", err)
	}
	if _, err := eng.CorrectFact(engine.CorrectRequest{
		RequestID: "user-fix",
		Scope:     engine.ScopeUser,
		Name:      "user",
		Expected:  revision,
		Body:      "prefer spaces",
	}); err != nil {
		t.Fatalf("current user correction: %v", err)
	}
	content, err := store.ReadUser()
	if err != nil {
		t.Fatalf("read user memory: %v", err)
	}
	if strings.TrimSpace(content) != "prefer spaces" {
		t.Fatalf("user memory = %q, want the correction", content)
	}
}

func TestSessionCursorsUseSeparateFiles(t *testing.T) {
	eng, _, projectDir, _ := newTestEngine(t)
	first := engine.Cursor{HarnessID: "harness-a", SessionID: "session-1", Position: 10, Phase: "fact"}
	second := engine.Cursor{HarnessID: "harness-b", SessionID: "session-2", Position: 20, Phase: "digest"}
	if err := eng.SaveCursor(first); err != nil {
		t.Fatalf("save first cursor: %v", err)
	}
	if err := eng.SaveCursor(second); err != nil {
		t.Fatalf("save second cursor: %v", err)
	}

	cursorName := func(c engine.Cursor) string {
		return memory.SlugKey(c.HarnessID, 48) + "--" + memory.SlugKey(c.SessionID, 48) + ".json"
	}
	firstPath := filepath.Join(projectDir, ".engine", "cursors", cursorName(first))
	secondPath := filepath.Join(projectDir, ".engine", "cursors", cursorName(second))
	if firstPath == secondPath {
		t.Fatal("distinct sessions must map to distinct cursor files")
	}
	firstData, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read first cursor file: %v", err)
	}
	secondData, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read second cursor file: %v", err)
	}
	if !strings.Contains(string(firstData), "fact") || !strings.Contains(string(secondData), "digest") {
		t.Fatalf("cursor files are not session specific: %q / %q", firstData, secondData)
	}
}
