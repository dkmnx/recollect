package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"recollect/internal/memory"
	"testing"
	"time"
)

// deterministicNow is the test clock.
var deterministicNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// newTestStore builds a store over a temp user dir and project dir with a
// deterministic clock. The project root is recorded so project.json can be
// written.
func newTestStore(t *testing.T, limits memory.Limits) (*memory.Store, string, string) {
	t.Helper()
	userDir := t.TempDir()
	projectDir := t.TempDir()
	store := memory.NewStore(memory.Options{
		UserDir:    userDir,
		ProjectDir: projectDir,
		Meta:       memory.ProjectMeta{Root: projectDir, Name: "proj", Slug: "proj"},
		Now:        func() time.Time { return deterministicNow },
		Limits:     limits,
	})
	return store, userDir, projectDir
}
