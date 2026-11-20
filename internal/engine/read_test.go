package engine_test

// audit: behavior - a read returns bytes and a revision derived from the same recovered file.
// audit: regression - recovery between the content read and the revision read let a correction overwrite unseen content.
// audit: coverage gap - existing read tests check the revision alone, not the bytes returned with it.
// audit: seam - Engine.Read with a pending intent written into the durable .engine state.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/memory"
)

func TestReadRevisionMatchesReturnedBytesAfterRecovery(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)
	added, err := eng.AppendFact(engine.AppendRequest{
		RequestID: "anchor-add", Description: "anchor", Body: "OLD BODY",
	})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact(added.Name)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.ToSlash(filepath.Join(fact.Path, fact.FileName()))
	target := filepath.Join(projectDir, filepath.FromSlash(rel))
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	changed := memory.FormatFact(memory.Fact{
		Name: fact.Name, Description: fact.Description, Type: fact.Type,
		Scene: fact.Scene, CapturedOn: fact.CapturedOn, Body: "RECOVERED BODY",
	})
	intent := txIntent{
		ID: "pending-read", Scope: "project", Op: "correct", Name: fact.Name, Path: rel,
		Before: revisionOf(rel, original), After: revisionOf(rel, []byte(changed)), Content: changed,
		Result: txResult{RequestID: "pending-read", Name: fact.Name, Revision: revisionOf(rel, []byte(changed))},
	}
	writeJSON(t, filepath.Join(projectDir, ".engine", "tx", stateIDFor(intent.ID)+".intent.json"), intent)

	got, err := eng.Read(engine.ScopeProject, fact.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "RECOVERED BODY") {
		t.Fatalf("read content = %q, want recovered body", got.Content)
	}
	if got.Revision != engine.Revision(revisionOf(rel, []byte(changed))) {
		t.Fatalf("revision = %s, want revision of recovered bytes", got.Revision)
	}
}

// audit: behavior - the engine user read keeps the store symlink and regular-file checks.
// audit: regression - os.ReadFile in Engine.Read followed a symlink that Store.ReadUser refused.
// audit: coverage gap - the read/revision test used a regular user.md only.
// audit: seam - Engine.Read with a symlinked user.md in a temporary user directory.
func TestReadUserRejectsSymlink(t *testing.T) {
	eng, _, _, userDir := newTestEngine(t)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("private via symlink"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(userDir, "user.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := eng.Read(engine.ScopeUser, ""); err == nil {
		t.Fatal("user read followed a symlink")
	}
}

// audit: behavior - engine reads reuse store parsing for facts without frontmatter.
// audit: regression - ParseFact alone returned empty name and description for a handwritten note.
// audit: coverage gap - the read/revision test used a formatted fact with frontmatter.
// audit: seam - Engine.Read and Store.ReadFact over a plain Markdown file.
func TestReadFactKeepsHandwrittenFallbackMetadata(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "plain.md"), []byte("# A plain note\nbody line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := store.ReadFact("plain")
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Read(engine.ScopeProject, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if got.Fact == nil || got.Fact.Name != want.Name || got.Fact.Description != want.Description {
		t.Fatalf("engine fact = %+v, want name %q and description %q", got.Fact, want.Name, want.Description)
	}
}

// audit: behavior - engine reads use the selected file's real path for a handwritten note whose name differs by case.
// audit: regression - reconstructing guide.md from the normalized name missed Guide.md on a case-sensitive filesystem.
// audit: coverage gap - the handwritten-note test used a lowercase file name.
// audit: seam - Engine.Read and Store.ReadFact over a mixed-case Markdown file.
func TestReadFactUsesSelectedFilePath(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Guide.md"), []byte("# Guide\nbody line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFact("guide"); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Read(engine.ScopeProject, "guide")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "# Guide") {
		t.Fatalf("engine content = %q, want the handwritten note bytes", got.Content)
	}
	if got.Revision == "" {
		t.Fatal("engine returned an empty revision")
	}
}

// audit: behavior - a correction after reading a mixed-case handwritten fact targets the same file.
// audit: regression - factRel rebuilt guide.md from the slug, so correcting Guide.md failed or hit a new file.
// audit: coverage gap - the read test checked content only, not the write path that consumes the revision.
// audit: seam: Engine.Read followed by Engine.CorrectFact over a mixed-case Markdown file.
func TestCorrectAfterReadingMixedCaseFact(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Guide.md"), []byte("# Guide\nold body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Read(engine.ScopeProject, "guide")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.CorrectFact(engine.CorrectRequest{
		RequestID: "correct-guide", Name: "guide", Expected: got.Revision,
		Description: "updated", Body: "new body",
	}); err != nil {
		t.Fatalf("CorrectFact after read: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(projectDir, "Guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "new body") {
		t.Fatalf("target file not updated: %q", raw)
	}
	if !caseInsensitiveDir(t, projectDir) {
		if _, err := os.Lstat(filepath.Join(projectDir, "guide.md")); !os.IsNotExist(err) {
			t.Fatalf("lowercase duplicate created: %v", err)
		}
	}
	fact, err := store.ReadFact("guide")
	if err != nil || fact.Body != "new body" {
		t.Fatalf("fact after correction = %+v, %v", fact, err)
	}
}

// caseInsensitiveDir reports whether the filesystem treats case variants as
// one name. Windows and default macOS do; Linux does not.
func caseInsensitiveDir(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "case-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}
