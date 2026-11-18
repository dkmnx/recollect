// fact_links_test.go — link resolution: one hop against the live store,
// root wins on name collisions, dangling targets come back as data.

package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"testing"
)

func TestResolveLinks(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "hub", Description: "hub fact", Body: "hub"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "spoke", Description: "spoke fact", Body: "spoke", Links: []string{"hub", "ghost"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "dup", Description: "root twin", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "dup", Description: "topic twin", Body: "y", Path: "topics/ops"}); err != nil {
		t.Fatal(err)
	}

	related, dangling, err := store.ResolveLinks("spoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Name != "hub" || related[0].Description != "hub fact" {
		t.Fatalf("related = %+v", related)
	}
	if len(dangling) != 1 || dangling[0] != "ghost" {
		t.Fatalf("dangling = %v", dangling)
	}

	related, dangling, err = store.ResolveLinks("dup")
	if err != nil || len(related) != 0 || dangling != nil {
		t.Fatalf("unlinked fact = %+v %v (%v)", related, dangling, err)
	}

	if _, _, err := store.ResolveLinks("nope"); err == nil || memory.CodeOf(err) != memory.CodeFactNotFound {
		t.Fatalf("missing fact = %v", err)
	}
}

func TestCountDanglingLinks(t *testing.T) {
	store, _, _ := newTestStore(t, memory.Limits{})
	if _, err := store.AppendFact(memory.Fact{Name: "hub", Description: "hub fact", Body: "hub"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "spoke", Description: "spoke fact", Body: "spoke", Links: []string{"hub", "ghost", "other-ghost"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendFact(memory.Fact{Name: "plain", Description: "no links", Body: "plain"}); err != nil {
		t.Fatal(err)
	}
	count, err := store.CountDanglingLinks()
	if err != nil || count != 2 {
		t.Fatalf("dangling = %d (%v)", count, err)
	}
}
