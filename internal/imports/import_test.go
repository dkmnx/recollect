package imports_test

// audit: behavior - Stage 6a copy-first import copies format-v1 state into a target store with hashes, mtimes, collisions, and status.
// audit: regression - memory store readers and writer-built sources keep their format-v1 contracts.
// audit: coverage gap - repeated import, dry run, external target edits, capture evidence, and manifest status.
// audit: seam - tests build sources with memory.NewStore writers and call imports.Run/Status on real directories.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/imports"
	"github.com/dkmnx/recollect/internal/memory"
)

var fixtureStamp = time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)

func buildSource(t *testing.T) imports.Source {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "source-project")
	user := filepath.Join(base, "source-user")
	store := memory.NewStore(memory.Options{
		ProjectDir: project,
		UserDir:    user,
		Meta:       memory.ProjectMetaFor(project),
	})
	if _, err := store.AppendFact(memory.Fact{
		Name: "deploy-port", Description: "Deploy port", Body: "The deploy port is 8443.",
		OriginSessionID: "old-session-1", CapturedOn: "2020-03-04",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{
		Name: "topic-fact", Description: "Topic fact", Body: "Topic body.", Path: "topics/ops",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendUser("initial import", "Prefer Go and small commits."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteDigest(memory.Digest{
		SessionID: "old-session-1", Topic: "Handoff", Summary: "Old session summary", Date: fixtureStamp, Updated: fixtureStamp,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertPage(memory.Page{
		Name: "core-concepts", Question: "Core concepts", Body: "The deploy port is 8443.", Sources: []string{"deploy-port"}, UpdatedOn: "2020-03-04",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendChainRevisions([]memory.ChainRevision{{
		Path: "deploy-port.md", SessionID: "old-session-1", At: fixtureStamp, OldText: "8080", NewText: "8443",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteCaptureState(memory.CaptureState{
		LastConsolidatedMessage: 12, SessionID: "old-session-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteCaptureAttempt(memory.CapturePhaseTyped, memory.CaptureAttempt{
		Phase: memory.CapturePhaseTyped, At: fixtureStamp, Attempts: 2, Bytes: 120,
	}); err != nil {
		t.Fatal(err)
	}
	return imports.Source{ProjectDir: project, UserDir: user}
}

func targetDirs(t *testing.T) (string, string, *memory.Store) {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "target-project")
	user := filepath.Join(base, "target-user")
	store := memory.NewStore(memory.Options{
		ProjectDir: project,
		UserDir:    user,
		Meta:       memory.ProjectMetaFor(project),
	})
	return project, user, store
}

func runImport(t *testing.T, source imports.Source, project, user string, target *memory.Store, dryRun bool) imports.Report {
	t.Helper()
	report, err := imports.Run(imports.Options{
		Source: source, Target: target, TargetDir: project, TargetUserDir: user, DryRun: dryRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestImportCopiesSupportedState(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	before := listFiles(t, source.ProjectDir)
	beforeUser := listFiles(t, source.UserDir)

	report := runImport(t, source, project, user, target, false)

	facts, err := target.ListFacts()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fact := range facts {
		names[fact.Name] = true
	}
	if !names["deploy-port"] || !names["topic-fact"] {
		t.Fatalf("facts not imported: %v", names)
	}
	userText, err := target.ReadUser()
	if err != nil || !strings.Contains(userText, "Prefer Go and small commits.") {
		t.Fatalf("user text not imported: %q %v", userText, err)
	}
	digests, err := target.ListDigests()
	if err != nil || len(digests) != 1 || digests[0].SessionID != "old-session-1" {
		t.Fatalf("digests not imported: %+v %v", digests, err)
	}
	pages, err := target.ListPages()
	if err != nil || len(pages) != 1 || pages[0].Name != "core-concepts" {
		t.Fatalf("pages not imported: %+v %v", pages, err)
	}
	revisions, err := target.ChainRevisions("deploy-port.md", 10)
	if err != nil || len(revisions) != 1 || revisions[0].NewText != "8443" {
		t.Fatalf("chain not imported: %+v %v", revisions, err)
	}
	state, err := target.ReadCaptureState()
	if err != nil || state.LastConsolidatedMessage != 12 || state.SessionID != "old-session-1" {
		t.Fatalf("capture state not imported: %+v %v", state, err)
	}
	attempts, err := target.ReadCaptureAttempts()
	if err != nil || attempts.Typed.Attempts != 2 {
		t.Fatalf("capture attempt not imported: %+v %v", attempts, err)
	}
	if _, _, ok := target.JournalStatus(); !ok {
		t.Fatal("journal not imported")
	}
	index, err := target.ReadIndex()
	if err != nil || !strings.Contains(index, "deploy-port.md") {
		t.Fatalf("index not regenerated: %q %v", index, err)
	}

	kinds := map[string]bool{}
	for _, mapping := range report.Mappings {
		kinds[mapping.Kind] = true
		if mapping.Hash == "" {
			t.Fatalf("mapping without hash: %+v", mapping)
		}
	}
	for _, kind := range []string{"fact", "user", "digest", "page", "chain", "capture", "journal", "index"} {
		if !kinds[kind] {
			t.Fatalf("missing %s mapping in %v", kind, kinds)
		}
	}
	if len(report.Applied) == 0 {
		t.Fatal("first import applied nothing")
	}
	if !reflectEqual(before, listFiles(t, source.ProjectDir)) || !reflectEqual(beforeUser, listFiles(t, source.UserDir)) {
		t.Fatal("source tree changed")
	}
}

func reflectEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportPreservesSourceMtimes(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)

	for _, root := range []string{source.ProjectDir, source.UserDir} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return os.Chtimes(path, fixtureStamp, fixtureStamp)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	report := runImport(t, source, project, user, target, false)
	for _, mapping := range report.Mappings {
		if mapping.Kind == "index" {
			continue
		}
		srcInfo, err := os.Stat(mapping.From)
		if err != nil {
			t.Fatal(err)
		}
		dstInfo, err := os.Stat(mapping.To)
		if err != nil {
			t.Fatal(err)
		}
		if !dstInfo.ModTime().UTC().Equal(srcInfo.ModTime().UTC()) {
			t.Fatalf("mtime not preserved for %s: source %v target %v", mapping.Kind, srcInfo.ModTime(), dstInfo.ModTime())
		}
	}
}

func TestImportRepeatedAppliesNothingNew(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	first := runImport(t, source, project, user, target, false)
	filesAfterFirst := listFiles(t, project)
	hashAfterFirst := hashTree(t, project)

	second := runImport(t, source, project, user, target, false)

	if len(second.Applied) != 0 {
		t.Fatalf("second import applied %v", second.Applied)
	}
	if len(second.Mappings) != len(first.Mappings) {
		t.Fatalf("mapping count changed: %d -> %d", len(first.Mappings), len(second.Mappings))
	}
	if !reflectEqual(filesAfterFirst, listFiles(t, project)) {
		t.Fatal("second import changed the target file set")
	}
	if hashAfterFirst != hashTree(t, project) {
		t.Fatal("second import changed target bytes")
	}
}

// audit: rollback keeps the untouched source store and leaves new shared facts in the target.
// regression: a rollback that exports new facts would violate the lossy rollback contract.
// gap: repeated import tests never wrote new shared data before switching back.
// seam: the test builds source and target stores on real directories and reads both.
func TestRollbackDrillSeparatesSourceAndSharedWrites(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	runImport(t, source, project, user, target, false)

	if _, err := target.AppendFact(memory.Fact{
		Name: "shared-only", Description: "Shared only", Body: "written after import",
	}); err != nil {
		t.Fatal(err)
	}

	rollbackStore := memory.NewStore(memory.Options{
		ProjectDir: source.ProjectDir,
		UserDir:    source.UserDir,
	})
	if _, err := rollbackStore.ReadFact("deploy-port"); err != nil {
		t.Fatalf("source fact missing after rollback: %v", err)
	}
	if _, err := rollbackStore.ReadFact("shared-only"); err == nil {
		t.Fatal("rollback source gained a shared-store fact")
	}
	if _, err := target.ReadFact("shared-only"); err != nil {
		t.Fatalf("shared fact lost after rollback: %v", err)
	}
}

func TestImportLeavesSourceUntouched(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	beforeProject := hashTree(t, source.ProjectDir)
	beforeUser := hashTree(t, source.UserDir)

	runImport(t, source, project, user, target, false)

	if beforeProject != hashTree(t, source.ProjectDir) {
		t.Fatal("import changed the source project store")
	}
	if beforeUser != hashTree(t, source.UserDir) {
		t.Fatal("import changed the source user store")
	}
}

func TestImportPicksUpSourceChanges(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	runImport(t, source, project, user, target, false)

	fact := filepath.Join(source.ProjectDir, "deploy-port.md")
	changed := "---\nname: deploy-port\ndescription: Deploy port\ncapturedOn: 2020-03-04\nmetadata:\n  type: project\n  node_type: memory\n---\n\nThe deploy port is 9443.\n"
	if err := os.WriteFile(fact, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	report := runImport(t, source, project, user, target, false)
	found := false
	for _, applied := range report.Applied {
		if strings.Contains(filepath.ToSlash(applied), "deploy-port.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed source was not applied: %v", report.Applied)
	}
	data, err := os.ReadFile(filepath.Join(project, "deploy-port.md"))
	if err != nil || string(data) != changed {
		t.Fatalf("target not updated: %q %v", data, err)
	}
}

func hashTree(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	for _, rel := range listFiles(t, root) {
		if strings.HasPrefix(rel, ".engine/imports/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(rel))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestImportDryRunWritesNothing(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)

	report := runImport(t, source, project, user, target, true)

	if !report.DryRun {
		t.Fatal("dry run flag not echoed")
	}
	if len(report.Mappings) == 0 {
		t.Fatal("dry run returned no mappings")
	}
	if len(report.Applied) != 0 {
		t.Fatalf("dry run applied %v", report.Applied)
	}
	if len(listFiles(t, project)) != 0 || len(listFiles(t, user)) != 0 {
		t.Fatalf("dry run wrote target files: %v %v", listFiles(t, project), listFiles(t, user))
	}
	if _, ok, err := imports.Status(project); err != nil || ok {
		t.Fatalf("dry run created a manifest: exists=%v err=%v", ok, err)
	}
}

func TestImportDryRunReportsCollisions(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	runImport(t, source, project, user, target, false)
	victim := filepath.Join(project, "deploy-port.md")
	if err := os.WriteFile(victim, []byte("externally edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := hashTree(t, project)

	report := runImport(t, source, project, user, target, true)

	found := false
	for _, collision := range report.Collisions {
		if strings.Contains(filepath.ToSlash(collision), "deploy-port.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("dry run did not report collision: %v", report.Collisions)
	}
	if len(report.Applied) != 0 {
		t.Fatalf("dry run applied %v", report.Applied)
	}
	if before != hashTree(t, project) {
		t.Fatal("dry run changed target bytes")
	}
}

func TestImportCollisionDoesNotOverwrite(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	runImport(t, source, project, user, target, false)

	victim := filepath.Join(project, "deploy-port.md")
	if err := os.WriteFile(victim, []byte("externally edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := runImport(t, source, project, user, target, false)

	found := false
	for _, collision := range report.Collisions {
		if strings.Contains(filepath.ToSlash(collision), "deploy-port.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no collision reported: %v", report.Collisions)
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "externally edited\n" {
		t.Fatalf("collision overwrote target: %q %v", data, err)
	}
	for _, applied := range report.Applied {
		if strings.Contains(filepath.ToSlash(applied), "deploy-port.md") {
			t.Fatal("colliding file listed as applied")
		}
	}
}

func TestImportCopiesCaptureEvidenceWithoutCursors(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	runImport(t, source, project, user, target, false)

	src, err := os.ReadFile(memory.CaptureStateFile(source.ProjectDir))
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.ReadFile(memory.CaptureStateFile(project))
	if err != nil || string(src) != string(dst) {
		t.Fatalf("capture state bytes changed: %v", err)
	}
	if !strings.Contains(string(dst), "old-session-1") {
		t.Fatalf("old session id missing: %s", dst)
	}
	cursors := filepath.Join(project, ".engine", "cursors")
	if files := listFiles(t, cursors); len(files) != 0 {
		t.Fatalf("importer created engine cursors: %v", files)
	}
}

func TestStatusReadsLastManifest(t *testing.T) {
	source := buildSource(t)
	project, user, target := targetDirs(t)
	if _, ok, err := imports.Status(project); err != nil || ok {
		t.Fatalf("status before import: exists=%v err=%v", ok, err)
	}
	report := runImport(t, source, project, user, target, false)
	stored, ok, err := imports.Status(project)
	if err != nil || !ok {
		t.Fatalf("status after import: exists=%v err=%v", ok, err)
	}
	if stored.SourceProjectDir != report.SourceProjectDir || len(stored.Mappings) != len(report.Mappings) {
		t.Fatalf("stored report mismatch: %+v vs %+v", stored, report)
	}
}
