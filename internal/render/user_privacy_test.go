package render_test

// audit: behavior - prompt rendering filters the complete user file before taking its injected tail.
// audit: regression - a tail that started inside a private span exposed the rest of that span to the model.
// audit: coverage gap - existing prompt tests use short user text, so the cut never split a private span.
// audit: seam - render.MemorySection with a store whose user file exceeds the inject limit.
import (
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
	"github.com/dkmnx/recollect/internal/render"
)

func TestMemorySectionFiltersUserTailSource(t *testing.T) {
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	user := strings.Repeat("x", 30_000) + "<private>\nLEAKED-SECRET-FROM-TAIL\n</private>\n"
	writeMemoryFile(t, memory.UserFile(memory.MemoryRoot(agentDir)), user)
	store := memory.NewStore(memory.Options{
		UserDir:    memory.MemoryRoot(agentDir),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMetaFor(projectDir),
		Limits:     memory.Limits{UserInjectBytes: 200},
	})

	section := render.MemorySection(render.Config{Enabled: true, Store: store, Root: projectDir})
	if strings.Contains(section, "LEAKED-SECRET-FROM-TAIL") {
		t.Fatalf("private text reached the rendered prompt: %q", section)
	}
}
