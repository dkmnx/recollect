package memory_test

// audit: behavior - measures Store.SearchFacts on fixed public queries before and after the SQLite backend.
// audit: regression - a backend swap must not hide a latency or allocation increase behind correctness tests.
// audit: coverage gap - the rank corpus asserts result order but records no timing or allocation baseline.
// audit: seam - the benchmark calls the public Store API with a deterministic generated corpus.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

var benchQueries = []struct {
	name, query, scene string
}{
	{"selective", "configuration", ""},
	{"broad", "memory", ""},
	{"scened", "configuration", "runtime"},
	{"blank", "", ""},
	{"stopwords", "the and of", ""},
	{"han", "配置", ""},
	{"fullwidth", "ｃｏｎｆｉｇｕｒａｔｉｏｎ", ""},
	{"substring", "onfigurat", ""},
}

func benchCorpus(b *testing.B, facts int) *memory.Store {
	b.Helper()
	store := memory.NewStore(memory.Options{
		ProjectDir: b.TempDir(),
		UserDir:    b.TempDir(),
		Meta:       memory.ProjectMetaFor("bench"),
	})
	for i := 0; i < facts; i++ {
		scene := ""
		if i%3 == 0 {
			scene = "runtime"
		}
		body := fmt.Sprintf("fact %d local configuration notes ", i) +
			strings.Repeat("and project memory details. ", 40)
		if _, err := store.AppendFact(memory.Fact{
			Name:        fmt.Sprintf("fact-%04d", i),
			Description: fmt.Sprintf("configuration topic %d", i),
			Body:        body,
			Scene:       scene,
		}); err != nil {
			b.Fatal(err)
		}
	}
	return store
}

func BenchmarkSearchFacts(b *testing.B) {
	for _, size := range []struct {
		name  string
		facts int
	}{{"small", 100}, {"large", 1000}} {
		b.Run(size.name, func(b *testing.B) {
			store := benchCorpus(b, size.facts)
			for _, query := range benchQueries {
				b.Run(query.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := store.SearchFacts(query.query, query.scene, 20); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
