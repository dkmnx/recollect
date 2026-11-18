package engine_test

// audit: behavior - engine append preserves adapter-supplied fact metadata.
// audit: regression - a facade route that drops scene, links, origin, or topic loses tested adapter behavior.
// audit: coverage - scene, links, origin, type, and topic-path preservation through the engine transaction.
// audit: seam - public engine and memory APIs over a real on-disk store.
import (
	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/memory"
	"path/filepath"
	"testing"
)

func TestAppendFactPreservesAdapterMetadata(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)

	added, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "metadata-1",
		Name:        "experience-fact",
		Description: "an experience",
		Fact: &memory.Fact{
			Description:     "an experience",
			Type:            memory.FactTypeExperience,
			Scene:           memory.FactSceneCoding,
			Body:            "what worked",
			OriginSessionID: "session-42",
			Links:           []string{"source-fact"},
		},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	fact, err := store.ReadFact(added.Name)
	if err != nil {
		t.Fatalf("read fact: %v", err)
	}
	if fact.Type != memory.FactTypeExperience {
		t.Errorf("type = %q, want %q", fact.Type, memory.FactTypeExperience)
	}
	if fact.Scene != memory.FactSceneCoding {
		t.Errorf("scene = %q, want %q", fact.Scene, memory.FactSceneCoding)
	}
	if fact.OriginSessionID != "session-42" {
		t.Errorf("origin = %q, want session-42", fact.OriginSessionID)
	}
	if len(fact.Links) != 1 || fact.Links[0] != "source-fact" {
		t.Errorf("links = %v, want [source-fact]", fact.Links)
	}
	if fact.CapturedOn == "" {
		t.Error("captured-on date is empty")
	}

	topic, err := eng.AppendFact(engine.AppendRequest{
		RequestID:   "metadata-2",
		Name:        "topic-fact",
		Description: "a topic fact",
		Fact: &memory.Fact{
			Description: "a topic fact",
			Body:        "topic body",
			Path:        "topics/ops",
		},
	})
	if err != nil {
		t.Fatalf("topic append: %v", err)
	}
	if _, err := store.ReadFact(topic.Name); err != nil {
		t.Fatalf("read topic fact: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(projectDir, "topics", "ops", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("topic fact was not written under topics/ops")
	}
}
