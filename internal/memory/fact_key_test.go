package memory_test

// audit: behavior - appends with a name at the length cap return unique names whose facts can be read back.
// audit: regression - a collision suffix pushed past the parser cap made a later append overwrite an earlier fact.
// audit: coverage gap - existing append tests use short names, so the suffix never exceeded the cap.
// audit: seam - Store.AppendFact and Store.ReadFact over a temporary project directory.
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

func TestAppendFactReservesRoomForCollisionSuffix(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	base := strings.Repeat("a", 48)
	seen := map[string]bool{}
	for i := 1; i <= 3; i++ {
		wantBody := fmt.Sprintf("body %d", i)
		written, err := store.AppendFact(memory.Fact{Name: base, Description: wantBody, Body: wantBody})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if len(written.Name) > 48 {
			t.Fatalf("append %d returned name longer than the parser cap: %q", i, written.Name)
		}
		if seen[written.Name] {
			t.Fatalf("append %d reused name %q", i, written.Name)
		}
		seen[written.Name] = true
		fact, err := store.ReadFact(written.Name)
		if err != nil {
			t.Fatalf("append %d name %q cannot be read back: %v", i, written.Name, err)
		}
		if fact.Body != wantBody {
			t.Fatalf("append %d name %q body = %q, want %q", i, written.Name, fact.Body, wantBody)
		}
	}
}

// audit: behavior - moving a fact with a maximum-length name keeps its name and inbound links.
// audit: regression - reserving suffix space on every NextKey call renamed free long facts and broke their links.
// audit: coverage gap - the new append test did not exercise MoveFactToTopic.
// audit: seam - Store.AppendFact, Store.MoveFactToTopic, and Store.ReadFact over a temporary project.
func TestMoveFactKeepsLongNameAndInboundLink(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	base := strings.Repeat("a", 48)
	if _, err := store.AppendFact(memory.Fact{Name: base, Description: "source", Body: "source body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "linker", Description: "links to source", Body: "linker body", Links: []string{base}}); err != nil {
		t.Fatal(err)
	}
	moved, err := store.MoveFactToTopic(base, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Name != base {
		t.Fatalf("moved name = %q, want %q", moved.Name, base)
	}
	linker, err := store.ReadFact("linker")
	if err != nil {
		t.Fatal(err)
	}
	if len(linker.Links) != 1 || linker.Links[0] != base {
		t.Fatalf("inbound links after move = %v, want %q", linker.Links, base)
	}
}

// audit: behavior - appending a fact copied from disk resolves a new destination and leaves the source intact.
// audit: regression - a copied fact carried the source relPath, so the append overwrote the original file.
// audit: coverage gap - append tests always built fresh Fact values without a disk path.
// audit: seam: Store.AppendFact with a fact returned by Store.ReadFact and then renamed.
func TestAppendCopiedFactKeepsSourceFile(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "first", Description: "first note", Body: "first body"}); err != nil {
		t.Fatal(err)
	}
	first, err := store.ReadFact("first")
	if err != nil {
		t.Fatal(err)
	}
	copy := first
	copy.Name = "second"
	copy.Description = "second note"
	copy.Body = "second body"
	second, err := store.AppendFact(copy)
	if err != nil {
		t.Fatal(err)
	}
	if second.Name != "second" {
		t.Fatalf("returned name = %q", second.Name)
	}
	before, err := os.ReadFile(filepath.Join(projectDir, first.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "first body") {
		t.Fatalf("source fact was overwritten: %q", before)
	}
	after, err := os.ReadFile(filepath.Join(projectDir, second.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "second body") {
		t.Fatalf("new fact missing: %q", after)
	}
}

// audit: behavior - upserting a mixed-case handwritten fact updates the selected file.
// audit: regression - a rebuilt lowercase path created a duplicate and left the original untouched.
// audit: coverage gap - the upsert-in-place test used engine-written lowercase files.
// audit: seam - Store.UpsertFact over a mixed-case Markdown file.
func TestUpsertKeepsMixedCaseFilePath(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	path := filepath.Join(projectDir, "Guide.md")
	if err := os.WriteFile(path, []byte("# Guide\nold body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertFact(memory.Fact{Name: "guide", Description: "updated", Body: "new body"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "new body") {
		t.Fatalf("selected file not updated: %q", raw)
	}
	if _, err := os.Lstat(filepath.Join(projectDir, "guide.md")); !os.IsNotExist(err) {
		t.Fatalf("lowercase duplicate created: %v", err)
	}
}

// audit: behavior - reserved store file names are rejected as fact names.
// audit: regression - a fact named memory wrote memory.md, which the index overwrote.
// audit: coverage gap - no append test reserved store-owned file names.
// audit: seam - Store.AppendFact with a reserved name.
func TestAppendFactRejectsReservedNames(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	for _, name := range []string{"memory", "corrections"} {
		if _, err := store.AppendFact(memory.Fact{Name: name, Description: name, Body: name + " body"}); err == nil {
			t.Fatalf("reserved name %q was accepted", name)
		}
	}
}

// audit: behavior - a valid summary.md fact stays readable.
// audit: regression - the legacy .summary.md name was matched as summary.md and hidden from the store.
// audit: coverage gap - reserved-name tests never used a legitimate summary fact.
// audit: seam - Store.ReadFact over a summary.md fact file.
func TestSummaryFactStaysReadable(t *testing.T) {
	store, _, projectDir := newTestStore(t, memory.Limits{})
	content := "---\nname: summary\ndescription: A summary fact\n---\n\nBody text.\n"
	if err := os.WriteFile(filepath.Join(projectDir, "summary.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("summary")
	if err != nil {
		t.Fatalf("summary fact unreadable: %v", err)
	}
	if fact.Description != "A summary fact" {
		t.Fatalf("summary fact = %+v", fact)
	}
}
