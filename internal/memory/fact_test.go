package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFactFullFrontmatter(t *testing.T) {
	raw, err := os.ReadFile("testdata/format-v1/fact-full.md")
	if err != nil {
		t.Fatal(err)
	}
	fact := memory.ParseFact(string(raw))
	if fact.Name != "xi-memory-format-v1" {
		t.Fatalf("name = %q", fact.Name)
	}
	if fact.Description != "xi project memory stores one fact per markdown file with YAML frontmatter" {
		t.Fatalf("description = %q", fact.Description)
	}
	if fact.Type != memory.FactTypeProject {
		t.Fatalf("type = %q", fact.Type)
	}
	if fact.OriginSessionID != "sess_ca25f4c2-1f09-45ad-9d50-a95615be4fe9" {
		t.Fatalf("originSessionId = %q", fact.OriginSessionID)
	}
	if !strings.Contains(fact.Body, "**Why:** a generic reader") {
		t.Fatalf("body = %q", fact.Body)
	}
	if strings.Contains(fact.Body, "frontmatter") && strings.HasPrefix(strings.TrimSpace(fact.Body), "---") {
		t.Fatal("frontmatter leaked into the body")
	}
}

func TestParseFactFoldedDescription(t *testing.T) {
	fact := memory.ParseFact("---\nname: folded\ndescription: first part\n  second part\nmetadata:\n  type: feedback\n---\n\nbody\n")
	if fact.Description != "first part second part" {
		t.Fatalf("folded description = %q", fact.Description)
	}
	if fact.Type != memory.FactTypeFeedback {
		t.Fatalf("type = %q", fact.Type)
	}
}

func TestParseFactMinimal(t *testing.T) {
	raw, err := os.ReadFile("testdata/format-v1/fact-minimal.md")
	if err != nil {
		t.Fatal(err)
	}
	fact := memory.ParseFact(string(raw))
	if fact.Name != "" || fact.Description != "" {
		t.Fatalf("expected empty metadata, got %q / %q", fact.Name, fact.Description)
	}
	if !strings.Contains(fact.Body, "Plain note kept by hand.") {
		t.Fatalf("body = %q", fact.Body)
	}
}

func TestParseFactUnterminatedBlock(t *testing.T) {
	fact := memory.ParseFact("---\nname: broken\nno closing fence\n")
	if fact.Name != "" {
		t.Fatalf("unterminated block should be body, name = %q", fact.Name)
	}
	if !strings.Contains(fact.Body, "no closing fence") {
		t.Fatalf("body = %q", fact.Body)
	}
}

func TestFormatFactGolden(t *testing.T) {
	fact := memory.PrepareFactForTest(memory.Fact{
		Name:            "redis-tuning",
		Description:     "Redis needs maxmemory set in every environment",
		Type:            memory.FactTypeProject,
		Body:            "Set maxmemory-policy allkeys-lru.\n",
		OriginSessionID: "sess_1",
	})
	want := "---\nname: redis-tuning\ndescription: Redis needs maxmemory set in every environment\nmetadata:\n  type: project\n  node_type: memory\n  originSessionId: sess_1\n---\n\nSet maxmemory-policy allkeys-lru.\n"
	if got := memory.FormatFact(fact); got != want {
		t.Fatalf("FormatFact =\n%q\nwant\n%q", got, want)
	}
	round := memory.ParseFact(want)
	if round.Name != fact.Name || round.Description != fact.Description || round.Type != fact.Type || round.OriginSessionID != fact.OriginSessionID || round.Body != fact.Body {
		t.Fatalf("round trip = %+v", round)
	}
}

func TestParseFactCapturedOnAndLinks(t *testing.T) {
	content := "---\nname: deploy-flow\ndescription: deploys run through make release\n" +
		"capturedOn: 2026-10-09\n" +
		"links: redis-tuning, deploy-flow, Redis Tuning, redis-tuning, , ghost-note\n" +
		"metadata:\n  type: project\n---\n\nbody\n"
	fact := memory.ParseFact(content)
	if fact.CapturedOn != "2026-10-09" {
		t.Fatalf("capturedOn = %q", fact.CapturedOn)
	}
	if len(fact.Links) != 2 || fact.Links[0] != "redis-tuning" || fact.Links[1] != "ghost-note" {
		t.Fatalf("links = %v", fact.Links)
	}
}

func TestFormatFactCapturedOnAndLinks(t *testing.T) {
	fact := memory.PrepareFactForTest(memory.Fact{
		Name:            "deploy-flow",
		Description:     "deploys run through make release",
		Type:            memory.FactTypeProject,
		CapturedOn:      "2026-10-09",
		Links:           []string{"redis-tuning"},
		OriginSessionID: "sess_1",
		Body:            "make release then make smoke",
	})
	want := "---\nname: deploy-flow\ndescription: deploys run through make release\n" +
		"capturedOn: 2026-10-09\nlinks: redis-tuning\n" +
		"metadata:\n  type: project\n  node_type: memory\n  originSessionId: sess_1\n---\n\nmake release then make smoke\n"
	if got := memory.FormatFact(fact); got != want {
		t.Fatalf("FormatFact =\n%q\nwant\n%q", got, want)
	}
	round := memory.ParseFact(want)
	if round.CapturedOn != fact.CapturedOn || len(round.Links) != 1 || round.Links[0] != "redis-tuning" {
		t.Fatalf("round trip = %+v", round)
	}
}

func TestFormatFactOmitsEmptyDatesAndLinks(t *testing.T) {
	fact := memory.PrepareFactForTest(memory.Fact{Name: "plain", Description: "d", Body: "b"})
	got := memory.FormatFact(fact)
	if strings.Contains(got, "capturedOn") || strings.Contains(got, "links") {
		t.Fatalf("empty fields leaked into frontmatter: %q", got)
	}
}

func TestPrepareFactNormalizesLinks(t *testing.T) {
	fact := memory.PrepareFactForTest(memory.Fact{
		Name:        "deploy-flow",
		Description: "d",
		Links:       []string{"Redis Tuning", "", "deploy-flow", "redis tuning", "a", "b", "c", "d", "e", "f", "g", "h"},
	})
	if len(fact.Links) != memory.MaxFactLinksForTest {
		t.Fatalf("links cap violated: %v", fact.Links)
	}
	if fact.Links[0] != "redis-tuning" {
		t.Fatalf("links not slugged: %v", fact.Links)
	}
	for _, link := range fact.Links {
		if link == "deploy-flow" {
			t.Fatalf("self link kept: %v", fact.Links)
		}
	}
}

func TestPrepareFactFallbacks(t *testing.T) {
	fact := memory.PrepareFactForTest(memory.Fact{Description: "内存事实：缓存上限"})
	if fact.Name == "" || len(fact.Name) != 8 {
		t.Fatalf("non-ASCII description should hash-fallback, name = %q", fact.Name)
	}
	if fact.Type != memory.FactTypeProject {
		t.Fatalf("default type = %q", fact.Type)
	}
	again := memory.PrepareFactForTest(memory.Fact{Description: "内存事实：缓存上限"})
	if again.Name != fact.Name {
		t.Fatalf("fallback name unstable: %q vs %q", again.Name, fact.Name)
	}
	other := memory.PrepareFactForTest(memory.Fact{Description: "内存事实：另一个"})
	if other.Name == fact.Name {
		t.Fatalf("distinct descriptions collapsed to %q", fact.Name)
	}
}

func TestFactSlug(t *testing.T) {
	if got := memory.FactSlug("Redis Tuning Notes!"); got != "redis-tuning-notes" {
		t.Fatalf("slug = %q", got)
	}
	if got := memory.FactSlug(longString(80)); len(got) > memory.MaxFactNameBytesForTest {
		t.Fatalf("slug cap violated: %d", len(got))
	}
}

func longString(n int) string {
	return strings.Repeat("word ", n)
}

func TestTruncateIndex(t *testing.T) {
	short := "- [a](a.md) — one\n"
	if got := memory.TruncateIndex(short); got != strings.TrimSuffix(short, "\n") {
		t.Fatalf("small index changed: %q", got)
	}
	many := strings.Repeat("- [f](f.md) — fact\n", memory.IndexLineLimit+5)
	got := memory.TruncateIndex(many)
	if !strings.Contains(got, "WARNING: MEMORY.md is") || !strings.Contains(got, "lines (limit: 200)") {
		t.Fatalf("line-cap warning missing: %q", tail(got, 200))
	}
	if len(strings.Split(got, "\n")) > memory.IndexLineLimit+4 {
		t.Fatalf("line cap not applied: %d lines", len(strings.Split(got, "\n")))
	}
	// Characters over the cap with lines under it: the char-only message.
	big := "- [f](f.md) — " + strings.Repeat("x", 200) + "\n"
	big = strings.Repeat(big, 150)
	got = memory.TruncateIndex(big)
	if !strings.Contains(got, "limit: 25000 bytes") {
		t.Fatalf("char-cap warning missing: %q", tail(got, 200))
	}
	if len(got) > memory.IndexCharLimit+500 {
		t.Fatalf("char cap not applied: %d", len(got))
	}
	if got := memory.TruncateIndex(""); got != "" {
		t.Fatalf("empty index = %q", got)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestBuildIndexBlock(t *testing.T) {
	block := memory.BuildIndex(memory.IndexInput{
		Name:      "proj",
		Root:      "/root",
		IndexPath: "/memory/MEMORY.md",
		Index:     "- [a](a.md) — one\n",
		Digests: []memory.Digest{
			{SessionID: "s1", Topic: "topic", Summary: "summary"},
			{SessionID: "s0", Topic: "old", Summary: "old"},
		},
	})
	if !strings.Contains(block, `<project_memory name="proj" root="/root" path="/memory/MEMORY.md">`) {
		t.Fatalf("envelope missing: %q", block)
	}
	if !strings.Contains(block, "- [a](a.md) — one") {
		t.Fatalf("index line missing: %q", block)
	}
	if !strings.Contains(block, "sessions: s1 — topic: summary") || !strings.Contains(block, "(1 more)") {
		t.Fatalf("sessions line missing: %q", block)
	}
	if !strings.HasSuffix(block, "</project_memory>\n") {
		t.Fatalf("envelope not closed: %q", block)
	}
}

// A fallback digest summary is multi-line and the newest digest is the one the
// sessions line renders, so the collapse is load-bearing: uncollapsed, the
// second line lands on its own row inside the project_memory envelope.
func TestBuildIndexCollapsesAMultiLineDigestSummary(t *testing.T) {
	date := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	block := memory.BuildIndex(memory.IndexInput{
		Name: "proj", Root: "/root", IndexPath: "/memory/MEMORY.md",
		Digests: []memory.Digest{{
			SessionID: "s9", Topic: "multi", Date: date, Updated: date,
			Summary: "Asked: why does the retry ladder double count\nReached: because classify sets retryable first",
		}},
	})
	if !strings.Contains(block, "sessions: s9 — multi: Asked: why does the retry ladder double count Reached: because classify sets retryable first") {
		t.Fatalf("multi-line summary was not collapsed onto the sessions line: %q", block)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "Reached:") {
			t.Fatalf("a digest summary line spilled into the envelope: %q", block)
		}
	}
}

func TestRenderIndexOrder(t *testing.T) {
	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	facts := []memory.Fact{
		{Name: "older", Description: "b", ModTime: base},
		{Name: "newer", Description: "a", ModTime: base.Add(time.Hour)},
		{Name: "tie", Description: "c", ModTime: base},
	}
	rendered := memory.RenderIndex(facts)
	want := "- [newer](newer.md) — a\n- [older](older.md) — b\n- [tie](tie.md) — c\n"
	if rendered != want {
		t.Fatalf("RenderIndex =\n%q\nwant\n%q", rendered, want)
	}
}
