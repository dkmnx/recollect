package memory_test

// audit: behavior - user search filters the complete file before slicing it into lines and context.
// audit: regression - slicing first left the tail of a multiline private span searchable and visible.
// audit: coverage gap - existing search tests use single-line private spans only.
// audit: seam - Store.SearchUser over a temporary user.md with a multiline private span.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

func TestSearchUserFiltersBeforeSlicing(t *testing.T) {
	store, userDir, _ := newTestStore(t, memory.Limits{MaxSearchResults: 10})
	content := "public heading\n<private>\nTOPSECRET-alpha\nTOPSECRET-beta\n</private>\npublic tail\n"
	if err := os.WriteFile(filepath.Join(userDir, "user.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchUser("TOPSECRET")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		if strings.Contains(match.Text, "TOPSECRET") {
			t.Fatalf("private line leaked in match text: %q", match.Text)
		}
		for _, line := range match.Context {
			if strings.Contains(line, "TOPSECRET") {
				t.Fatalf("private line leaked in match context: %q", line)
			}
		}
	}
}
