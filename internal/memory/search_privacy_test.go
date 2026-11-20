package memory_test

// audit: behavior - project search matches only visible text after private spans and secret patterns are removed.
// audit: regression - facts or queries supported solely by private or secret text produced hits before this filter.
// audit: coverage gap - the rank corpus uses public text and never exercises filtered queries or cross-field spans.
// audit: seam - tests drive Store.SearchFacts and read its returned fields through the public Store API.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

func writeFactFile(t *testing.T, project, name, description, body string) {
	t.Helper()
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(project, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func searchNames(t *testing.T, store *memory.Store, query, scene string) []string {
	t.Helper()
	matches, err := store.SearchFacts(query, scene, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.Name)
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestSearchSkipsPrivateAndSecretOnlyFacts(t *testing.T) {
	store, _, project := newTestStore(t, memory.Limits{})
	writeFactFile(t, project, "public-fact", "visible public note", "plain body")
	writeFactFile(t, project, "private-fact", "<private>hidden topic</private>", "plain filler")
	writeFactFile(t, project, "secret-fact", "api key: AKIA1234567890ABCDEF", "only secret text")
	for _, query := range []string{"hidden", "AKIA1234567890ABCDEF", "api key"} {
		if names := searchNames(t, store, query, ""); len(names) != 0 {
			t.Fatalf("query %q matched %v", query, names)
		}
	}
	if names := searchNames(t, store, "visible", ""); !containsName(names, "public-fact") {
		t.Fatalf("public fact missing from %v", names)
	}
}

func TestSearchFiltersQueryAndKeepsVisibleRemainder(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	for _, fact := range []memory.Fact{
		{Name: "beta-fact", Description: "beta topic", Body: "visible beta body"},
		{Name: "secret-fact", Description: "world", Body: "api key: AKIA1234567890ABCDEF"},
	} {
		if _, err := store.AppendFact(fact); err != nil {
			t.Fatal(err)
		}
	}
	if names := searchNames(t, store, "<private>secret</private> beta", ""); !containsName(names, "beta-fact") {
		t.Fatalf("mixed query lost visible remainder: %v", names)
	}
	if names := searchNames(t, store, "<private>secret</private>", ""); len(names) != 0 {
		t.Fatalf("fully filtered query matched %v", names)
	}
	if names := searchNames(t, store, "api key: AKIA1234567890ABCDEF", ""); len(names) != 0 {
		t.Fatalf("secret-only query matched %v", names)
	}
}

func TestSearchMasksCrossFieldPrivateSpan(t *testing.T) {
	store, _, project := newTestStore(t, memory.Limits{})
	writeFactFile(t, project, "cross-fact", "visible <private>hidden", "secret</private> tail")
	names := searchNames(t, store, "visible", "")
	if !containsName(names, "cross-fact") {
		t.Fatalf("visible prefix did not match: %v", names)
	}
	for _, query := range []string{"hidden", "secret"} {
		if names := searchNames(t, store, query, ""); len(names) != 0 {
			t.Fatalf("cross-field private text leaked for %q: %v", query, names)
		}
	}
}

func TestSearchRedactionMarkerIsNotSearchable(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{
		Name: "marker-fact", Description: "api key: AKIA1234567890ABCDEF", Body: "plain tail",
	}); err != nil {
		t.Fatal(err)
	}
	if names := searchNames(t, store, "redacted", ""); len(names) != 0 {
		t.Fatalf("redaction marker is searchable: %v", names)
	}
	if names := searchNames(t, store, "plain", ""); !containsName(names, "marker-fact") {
		t.Fatalf("visible remainder missing: %v", names)
	}
}

// audit: behavior - search drops facts whose raw file name is a bare secret token.
// audit: regression - the name was slugged before the secret filter, so AKIA-style file names stayed searchable.
// audit: coverage gap - the raw-name filter ran only in the marker test, which NTFS cannot host.
// audit: seam - ListFacts over a store with an AKIA-named fact file, then SearchFacts for the name and its body.
func TestSearchFiltersSecretFileName(t *testing.T) {
	store, _, project := newTestStore(t, memory.Limits{})
	secretPath := project + "/AKIA1234567890ABCDEF.md"
	if err := os.WriteFile(secretPath, []byte("---\ndescription: plain\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := store.ListFacts()
	if err != nil || len(facts) != 1 {
		t.Fatalf("fixture facts = %d, err = %v", len(facts), err)
	}
	if names := searchNames(t, store, "plain", ""); len(names) != 0 {
		t.Fatalf("facts with filtered derived names were not skipped: %v", names)
	}
	if names := searchNames(t, store, "akia", ""); len(names) != 0 {
		t.Fatalf("secret file name leak: %v", names)
	}
}

// audit: behavior - search drops a fact whose raw file name carries a private marker.
// audit: regression - filtering the name after slug normalization stripped the markers and left the name searchable.
// audit: coverage gap - only a file name with a marker separates raw-name filtering from slug-then-filter.
// audit: seam - ListFacts over a store with a marker-named fact file, then SearchFacts for the marker word.
func TestSearchFiltersPrivateMarkerFileName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NTFS rejects angle brackets in file names; the fixture cannot exist there")
	}
	store, _, project := newTestStore(t, memory.Limits{})
	if err := os.WriteFile(project+"/<private>hidden.md",
		[]byte("---\ndescription: plain\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := store.ListFacts()
	if err != nil || len(facts) != 1 {
		t.Fatalf("fixture facts = %d, err = %v", len(facts), err)
	}
	if names := searchNames(t, store, "plain", ""); len(names) != 0 {
		t.Fatalf("facts with filtered derived names were not skipped: %v", names)
	}
	if names := searchNames(t, store, "hidden", ""); len(names) != 0 {
		t.Fatalf("marker file name leak: %v", names)
	}
}
