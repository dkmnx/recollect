package memory

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.
import "time"

// ScoredFactForTest is the ranked-fact view with exported fields, so a
// black-box test can read the score and the underlying fact without naming the
// unexported scoredFact. This file compiles into the test binary only and ships
// nowhere, so the package's exported surface is unchanged.
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
