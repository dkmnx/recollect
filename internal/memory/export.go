package memory

// audit: host-adapter compatibility exports for xi's black-box memory suite.
// regression: a divergent helper would hide behavior drift between the engines.
// gap: xi's memory suite used package-internal helpers before the facade swap.
// seam: these functions are exported so xi can call them through memory/memory.go.
import "time"

// ScoredFactForTest is the ranked-fact view with exported fields, so a
// black-box adapter can read the score and the underlying fact without naming
// the unexported scoredFact.
type ScoredFactForTest struct {
	Fact  Fact
	Score float64
	Field string
}

// RankFactsForTest runs the fact ranking and converts to the view above. A nil
// result stays nil, which the tests assert.
func RankFactsForTest(facts []Fact, query string, now time.Time) []ScoredFactForTest {
	ranked := rankFacts(facts, query, now)
	if ranked == nil {
		return nil
	}
	out := make([]ScoredFactForTest, len(ranked))
	for i, sf := range ranked {
		out[i] = ScoredFactForTest{Fact: sf.fact, Score: sf.score, Field: sf.field}
	}
	return out
}

// PrepareFactForTest exposes the fact normalizer.
func PrepareFactForTest(fact Fact) Fact { return prepareFact(fact) }

// ManifestKindRankForTest exposes the manifest kind ordering.
func ManifestKindRankForTest(kind string) int { return manifestKindRank(kind) }

// ContainPathForTest exposes the path containment check.
func ContainPathForTest(root, name string) (string, error) { return containPath(root, name) }

// DecodeJournalLineForTest exposes the journal line decoder.
func DecodeJournalLineForTest(line string) (JournalEntry, error) { return decodeJournalLine(line) }

// ExtractJSONObjectForTest exposes the JSON object slicer.
func ExtractJSONObjectForTest(raw string) (string, error) { return extractJSONObject(raw) }

// RepairJSONEscapesForTest exposes the escape repairer.
func RepairJSONEscapesForTest(payload string) string { return repairJSONEscapes(payload) }

// SlugifyForTest exposes the name slugifier.
func SlugifyForTest(name string) string { return slugify(name) }

// The caps and filenames the tests pin.
const (
	ArchiveDigestKeepForTest  = archiveDigestKeep
	JournalPrevFileForTest    = journalPrevFile
	JournalRotateBytesForTest = journalRotateBytes
	MaxFactLinksForTest       = maxFactLinks
	MaxFactNameBytesForTest   = maxFactNameBytes
)
