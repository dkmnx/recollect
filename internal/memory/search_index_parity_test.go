package memory_test

// audit: behavior - indexed and filtered file search return the same order, fields, paths, scenes, and limits.
// audit: regression - an FTS5 candidate set could drop a lexical hit or change the ranker's order.
// audit: coverage gap - the rank corpus exercises only the file backend.
// audit: seam - the test drives Store.SearchFacts with both backends over the public Store API.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func TestTokenFreeQueryParity(t *testing.T) {
	for _, backend := range []string{"", memory.SearchBackendSQLite} {
		store := corpusStore(t, backend)
		for _, fact := range []memory.Fact{
			{Name: "alpha-doc", Description: "alpha topic", Body: "alpha body"},
			{Name: "beta-doc", Description: "beta topic", Body: "beta body"},
		} {
			if _, err := store.AppendFact(fact); err != nil {
				t.Fatal(err)
			}
		}
		matches, err := store.SearchFacts("the and of", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 2 {
			t.Fatalf("backend %q token-free hits = %d, want 2", backend, len(matches))
		}
	}
}

func TestDuplicateNamesKeepDistinctPaths(t *testing.T) {
	for _, backend := range []string{"", memory.SearchBackendSQLite} {
		t.Run(backend, func(t *testing.T) {
			store := corpusStore(t, backend)
			project := store.ProjectDir()
			for name, body := range map[string]string{"a": "alpha body", "b": "beta body"} {
				content := "---\nname: same-name\ndescription: topic\n---\n\n" + body + "\n"
				if err := os.WriteFile(filepath.Join(project, name+".md"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			matches, err := store.SearchFacts("alpha", "", 0)
			if err != nil || len(matches) != 1 {
				t.Fatalf("duplicate-name search = %+v err=%v", matches, err)
			}
			if filepath.Base(matches[0].Path) != "a.md" {
				t.Fatalf("wrong path for duplicate name: %s", matches[0].Path)
			}
		})
	}
}

func corpusStore(t *testing.T, backend string) *memory.Store {
	t.Helper()
	project := t.TempDir()
	store := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: t.TempDir(),
		Meta:          memory.ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: backend,
	})
	return store
}

func TestRankCorpusIndexParity(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "rank-cases"))
	if err != nil {
		t.Fatal(err)
	}
	cases := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		cases++
		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "rank-cases", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var tc struct {
				Query string `json:"query"`
				Scene string `json:"scene"`
				Facts []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Body        string `json:"body"`
					Scene       string `json:"scene"`
				} `json:"facts"`
				Expected []string `json:"expected"`
			}
			if err := json.Unmarshal(raw, &tc); err != nil {
				t.Fatal(err)
			}
			fileStore := corpusStore(t, "")
			indexStore := corpusStore(t, memory.SearchBackendSQLite)
			for _, fact := range tc.Facts {
				for _, store := range []*memory.Store{fileStore, indexStore} {
					if _, err := store.AppendFact(memory.Fact{
						Name: fact.Name, Description: fact.Description,
						Body: fact.Body, Scene: fact.Scene,
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, limit := range []int{0, 5} {
				fileMatches, err := fileStore.SearchFacts(tc.Query, tc.Scene, limit)
				if err != nil {
					t.Fatal(err)
				}
				indexMatches, err := indexStore.SearchFacts(tc.Query, tc.Scene, limit)
				if err != nil {
					t.Fatal(err)
				}
				for _, matches := range [][]memory.FactMatch{fileMatches, indexMatches} {
					for i := range matches {
						matches[i].Path = filepath.Base(matches[i].Path)
					}
				}
				if !reflect.DeepEqual(fileMatches, indexMatches) {
					t.Fatalf("limit %d parity failure\nfile:  %+v\nindex: %+v", limit, fileMatches, indexMatches)
				}
			}
		})
	}
	if cases == 0 {
		t.Fatal("corpus dir holds no case files")
	}
}
