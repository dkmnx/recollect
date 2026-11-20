package memory_test

// audit: behavior - compares filtered file and SQLite candidate paths on the same fixed corpus.
// audit: regression - an index that reads all Markdown must still justify its cost over the file path.
// audit: coverage gap - separate benchmarks per backend would drift in corpus and environment.
// audit: seam - the benchmark drives Store.SearchFacts with both backends in one process.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

func backendStore(b *testing.B, backend string, facts int) *memory.Store {
	b.Helper()
	project := b.TempDir()
	store := memory.NewStore(memory.Options{
		ProjectDir: project, UserDir: b.TempDir(),
		Meta:          memory.ProjectMeta{Root: project, Name: "proj", Slug: "proj"},
		Now:           func() time.Time { return deterministicNow },
		SearchBackend: backend,
	})
	for i := 0; i < facts; i++ {
		body := fmt.Sprintf("fact %d local configuration notes ", i) +
			strings.Repeat("and project memory details. ", 40)
		if _, err := store.AppendFact(memory.Fact{
			Name:        fmt.Sprintf("fact-%04d", i),
			Description: fmt.Sprintf("configuration topic %d", i),
			Body:        body,
		}); err != nil {
			b.Fatal(err)
		}
	}
	return store
}

func BenchmarkBackendComparison(b *testing.B) {
	for _, backend := range []string{"", memory.SearchBackendSQLite} {
		b.Run(backend, func(b *testing.B) {
			store := backendStore(b, backend, 1000)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.SearchFacts("configuration", "", 20); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
