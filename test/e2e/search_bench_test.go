package e2e

// audit: behavior - measures stdio project search on fixed public queries before and after the SQLite backend.
// audit: regression - transport overhead must not mask a backend latency or allocation change.
// audit: coverage gap - transport tests assert results but record no timing baseline.
// audit: seam - the benchmark drives the built binary with JSON-RPC search frames.

import (
	"fmt"
	"strings"
	"testing"
)

var rpcBenchQueries = []struct {
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

func BenchmarkSearchRPC(b *testing.B) {
	body := strings.Repeat("Local configuration notes and project memory details. ", 40)
	for _, size := range []struct {
		name  string
		facts int
	}{{"small", 100}, {"large", 1000}} {
		b.Run(size.name, func(b *testing.B) {
			d := tempDirs(b)
			c := start(b)
			defer c.cmd.Process.Kill()
			initialize(b, c, d, "bench-harness", map[string]int{"major": 1})
			for i := 0; i < size.facts; i++ {
				scene := ""
				if i%3 == 0 {
					scene = "runtime"
				}
				ok(b, c.call(b, "memory.execute", execParams{
					Target: "project", Operation: "append",
					RequestID:   fmt.Sprintf("bench-%s-%d", size.name, i),
					Description: fmt.Sprintf("configuration topic %d", i),
					Body:        body, Scene: scene,
				}))
			}
			for _, query := range rpcBenchQueries {
				b.Run(query.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					params := execParams{
						Target: "project", Operation: "search",
						Query: query.query, Scene: query.scene, Limit: 20,
					}
					for i := 0; i < b.N; i++ {
						if _, err := c.callRaw("memory.execute", params); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
