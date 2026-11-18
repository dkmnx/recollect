package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"fmt"
	"github.com/dkmnx/recollect/internal/memory"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSanitizeAgentMemoryKey(t *testing.T) {
	cases := []struct{ name, want string }{
		{"general-purpose", "general-purpose"},
		{"Explore", "Explore"},               // case is kept
		{"code review!", "code-review-"},     // spaces and punctuation dash
		{"a/b\\c", "a-b-c"},                  // separators cannot smuggle a path
		{"multi   spaces", "multi---spaces"}, // each bad rune becomes one dash
		{"name_with.dots", "name_with-dots"}, // underscore and dash survive
		{"", "unknown"},                      // empty falls back
		{"   ", "---"},                       // whitespace is not special-cased
		{"память", "------"},                 // non-ASCII is dashed, not dropped
	}
	for _, tc := range cases {
		if got := memory.SanitizeAgentMemoryKey(tc.name); got != tc.want {
			t.Errorf("SanitizeAgentMemoryKey(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAgentMemoryRootLayout(t *testing.T) {
	// Native paths: "/tmp/agent" is not absolute on Windows, so the join would
	// produce "\tmp\agent" and the expected root would have to spell it back.
	agentDir := filepath.Join(string(filepath.Separator), "tmp", "agent")
	wantRoot := filepath.Join(agentDir, "memory", "agent-memory")
	if got := memory.AgentMemoriesRoot(agentDir); got != wantRoot {
		t.Errorf("AgentMemoriesRoot = %q, want %q", got, wantRoot)
	}
	key := memory.SanitizeAgentMemoryKey("general-purpose")
	wantKey := filepath.Join(wantRoot, key)
	if got := memory.AgentMemoryRoot(agentDir, key); got != wantKey {
		t.Errorf("AgentMemoryRoot = %q, want %q", got, wantKey)
	}
}

// TestUserAppendSerializesAcrossStores pins the per-path user.md lock: the
// app store and an agent store share the user file but hold separate
// instance mutexes, and without the path lock their AppendUser cycles
// interleave and lose appends.
func TestUserAppendSerializesAcrossStores(t *testing.T) {
	userDir := t.TempDir()
	appStore := memory.NewStore(memory.Options{UserDir: userDir, ProjectDir: t.TempDir()})
	agentStore := memory.NewStore(memory.Options{UserDir: userDir, ProjectDir: t.TempDir()})

	const perStore = 60
	var wg sync.WaitGroup
	for _, store := range []*memory.Store{appStore, agentStore} {
		wg.Add(1)
		go func(s *memory.Store) {
			defer wg.Done()
			for i := 0; i < perStore; i++ {
				if _, err := s.AppendUser("durable correction", "fact from a concurrent writer"); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(store)
	}
	wg.Wait()

	content, err := appStore.ReadUser()
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	if got := strings.Count(content, "mem-append-reason"); got != 2*perStore {
		t.Fatalf("user.md holds %d appends, want %d: two stores over one user.md lost writes", got, 2*perStore)
	}
}

func TestAgentMemoryPromptSectionTeachesAndCarriesIndex(t *testing.T) {
	section := memory.AgentMemoryPromptSection("- [build-check](build-check.md) — make check is the gate\n", nil)
	for _, want := range []string{
		"## Memory",
		"No-op is",
		"target=project",
		"applies across all projects",
		"target=project addresses your own agent memory directory",
		"report to the parent",
		"- [build-check](build-check.md) — make check is the gate",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("agent section lacks %q:\n%s", want, section)
		}
	}
}

func TestAgentMemoryPromptSectionEmptyIndexStillTeaches(t *testing.T) {
	section := memory.AgentMemoryPromptSection("", nil)
	if !strings.Contains(section, "## Memory") || !strings.Contains(section, "currently empty") {
		t.Fatalf("empty index must keep the contract:\n%s", section)
	}
	if strings.Contains(section, "WARNING") {
		t.Errorf("empty index must not warn:\n%s", section)
	}
}

func TestAgentMemoryPromptSectionReadErrorOmitsIndex(t *testing.T) {
	section := memory.AgentMemoryPromptSection("", fmt.Errorf("read failed"))
	if !strings.Contains(section, "## Memory") || !strings.Contains(section, "applies across all projects") {
		t.Fatalf("read error must keep the contract:\n%s", section)
	}
	if strings.Contains(section, "currently empty") || strings.Contains(section, "- [") {
		t.Fatalf("read error must not claim emptiness or show an index:\n%s", section)
	}
}

func TestAgentMemoryPromptSectionTruncatesIndex(t *testing.T) {
	var b strings.Builder
	for i := 0; i < memory.IndexLineLimit+10; i++ {
		b.WriteString(fmt.Sprintf("- [fact-%03d](f.md) — line\n", i))
	}
	section := memory.AgentMemoryPromptSection(b.String(), nil)
	if !strings.Contains(section, "WARNING: MEMORY.md is") {
		t.Fatalf("truncation warning missing:\n%s", section)
	}
	if got := strings.Count(section, "- [fact-"); got > memory.IndexLineLimit {
		t.Fatalf("index not truncated: %d fact lines", got)
	}
}
