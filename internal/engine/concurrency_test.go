package engine_test

// audit: behavior - Four processes append distinct facts to one project store and one user store.
// audit: regression - the index stays parseable and no child add is lost.
// audit: coverage gap - cross-process locking was not exercised by the process-local memory tests.
// audit: seam - the helper is a Go test process using only the public engine API.
import (
	"fmt"
	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/memory"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const helperEnv = "RECOLLECT_ENGINE_HELPER"

var workers = []struct {
	id   string
	word string
}{
	{"worker-1", "saffron"},
	{"worker-2", "marigold"},
	{"worker-3", "cerulean"},
	{"worker-4", "obsidian"},
}

var indexLineRE = regexp.MustCompile(`^- \[[a-z0-9-]+\]\([^)]+\) — .+$`)

func TestFourProcessesAppendWithoutLostUpdates(t *testing.T) {
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	userDir := filepath.Join(base, "user")

	errs := make(chan error, len(workers))
	var wg sync.WaitGroup
	for _, worker := range workers {
		wg.Add(1)
		go func(id, word string) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperAppendWorker")
			cmd.Env = append(os.Environ(),
				helperEnv+"=1",
				"RECOLLECT_PROJECT_DIR="+projectDir,
				"RECOLLECT_USER_DIR="+userDir,
				"RECOLLECT_REQUEST_ID="+id,
				"RECOLLECT_WORD="+word,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("worker %s: %v\n%s", id, err, out)
			}
		}(worker.id, worker.word)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	store := memory.NewStore(memory.Options{
		ProjectDir: projectDir,
		UserDir:    userDir,
		Meta:       memory.ProjectMetaFor(projectDir),
	})
	facts, err := store.ListFacts()
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if len(facts) != len(workers) {
		t.Fatalf("stored %d facts, want %d", len(facts), len(workers))
	}
	got := make(map[string]bool)
	for _, fact := range facts {
		got[fact.Description] = true
	}
	for _, worker := range workers {
		if !got[worker.word] {
			t.Fatalf("fact %q from %s is missing", worker.word, worker.id)
		}
	}

	index, err := store.ReadIndex()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(index), "\n") {
		if line != "" && !indexLineRE.MatchString(line) {
			t.Fatalf("unparseable index line: %q", line)
		}
	}
	for _, worker := range workers {
		if !strings.Contains(index, worker.word) {
			t.Fatalf("index is missing %q", worker.word)
		}
	}
}

func TestFourProcessesAppendUserMemory(t *testing.T) {
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	userDir := filepath.Join(base, "user")

	var wg sync.WaitGroup
	errs := make(chan error, len(workers))
	for _, worker := range workers {
		wg.Add(1)
		go func(id, word string) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperAppendWorker")
			cmd.Env = append(os.Environ(),
				helperEnv+"=1",
				"RECOLLECT_SCOPE=user",
				"RECOLLECT_PROJECT_DIR="+projectDir,
				"RECOLLECT_USER_DIR="+userDir,
				"RECOLLECT_REQUEST_ID="+id,
				"RECOLLECT_WORD="+word,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("user worker %s: %v\n%s", id, err, out)
			}
		}(worker.id, worker.word)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	store := memory.NewStore(memory.Options{ProjectDir: projectDir, UserDir: userDir})
	content, err := store.ReadUser()
	if err != nil {
		t.Fatalf("read user memory: %v", err)
	}
	for _, worker := range workers {
		if !strings.Contains(content, worker.word) {
			t.Fatalf("user memory is missing %q", worker.word)
		}
	}
}

func TestHelperAppendWorker(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	projectDir := os.Getenv("RECOLLECT_PROJECT_DIR")
	userDir := os.Getenv("RECOLLECT_USER_DIR")
	word := os.Getenv("RECOLLECT_WORD")
	store := memory.NewStore(memory.Options{
		ProjectDir: projectDir,
		UserDir:    userDir,
		Meta:       memory.ProjectMetaFor(projectDir),
	})
	eng := engine.Open(engine.Config{
		Store:       store,
		ProjectDir:  projectDir,
		UserDir:     userDir,
		LockTimeout: 5 * time.Second,
	})
	req := engine.AppendRequest{
		RequestID:   os.Getenv("RECOLLECT_REQUEST_ID"),
		Description: word,
		Body:        word + " body",
	}
	if os.Getenv("RECOLLECT_SCOPE") == "user" {
		req.Scope = engine.ScopeUser
		req.Reason = "concurrent worker"
	}
	if _, err := eng.AppendFact(req); err != nil {
		t.Fatalf("worker append: %v", err)
	}
}
