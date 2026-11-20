// page_evolve.go — the page pass's reply contract: parse the model's page
// drafts through the shared tolerant JSON extractor, then police them
// against the fact snapshot so a page is never born stale (a cited fact
// that does not exist reads as eternal staleness) and never carries a
// secret into the injected prompt.

package memory

import (
	"strings"
)

// EvolvePagesPrompt asks for the standing pages the sweep requested. The
// schema is its own envelope: pages are derived state, not fact ops, so
// they never ride the capture op vocabulary.
const EvolvePagesPrompt = `You are xi's memory page writer.
Compose the standing project pages requested below from the fact excerpts given.
Return JSON only. Do not explain.

Each request names one page, its standing question, and excerpts of the facts you may cite. Compose the page body from those excerpts only; never invent a fact, a name, or content. Cite exactly the facts you used, comma-separated, in sources. A page that cannot cite at least one fact must be omitted. Keep each body under 2000 bytes: the answer itself, no preamble.

Output schema:
{"pages":[{"name":"catalog-slug","sources":"fact-a, fact-b","content":"the composed answer"}],"reason":"why nothing was composed"}`

// PageDraft is one page op parsed from a page-pass reply.
type PageDraft struct {
	Name    string
	Sources LinksField
	Content string
}

// PageDraftResult is a parsed page reply. Dropped counts the drafts
// normalization rejected: a slug outside the catalog, or no content.
type PageDraftResult struct {
	Drafts  []PageDraft
	Reason  string
	Dropped int
}

// ParsePageDrafts parses the model's page JSON, tolerating code fences,
// surrounding prose, and near-JSON through the shared capture extractor.
func ParsePageDrafts(raw string) (PageDraftResult, error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return PageDraftResult{}, err
	}
	var envelope struct {
		Pages []struct {
			Name    string     `json:"name"`
			Sources LinksField `json:"sources"`
			Content string     `json:"content"`
		} `json:"pages"`
		Reason string `json:"reason"`
	}
	if err := decodeCaptureJSON(payload, &envelope); err != nil {
		return PageDraftResult{}, Errorf(CodeInvalidReply, "page reply is not valid JSON")
	}
	result := PageDraftResult{Reason: strings.TrimSpace(envelope.Reason)}
	for _, page := range envelope.Pages {
		name := FactSlug(page.Name)
		if _, ok := PageQuestion(name); !ok {
			result.Dropped++
			continue
		}
		content := strings.TrimSpace(page.Content)
		if content == "" {
			result.Dropped++
			continue
		}
		result.Drafts = append(result.Drafts, PageDraft{
			Name:    name,
			Sources: normalizePageSources(page.Sources),
			Content: content,
		})
	}
	return result, nil
}

// FilterPageDrafts polices parsed drafts against the fact-name snapshot a
// caller took after the sweep's ops applied, and against the slugs the
// pass actually requested, so a chatty reply cannot rewrite pages the
// bound never asked for. A source the snapshot does not name is stripped
// from the draft; a draft left without any valid source is skipped, so
// every sweep-written page cites at least one fact that exists. A
// secret-bearing body is skipped. The skipped count lets the caller
// report what the reply tried outside its contract.
func FilterPageDrafts(result PageDraftResult, factNames, requested map[string]bool) (pages []Page, skipped int) {
	for _, draft := range result.Drafts {
		if !requested[draft.Name] {
			skipped++
			continue
		}
		if SecretLike(draft.Content) {
			skipped++
			continue
		}
		page := Page{Name: draft.Name, Body: draft.Content}
		for _, source := range draft.Sources {
			if factNames[source] {
				page.Sources = append(page.Sources, source)
			}
		}
		if len(page.Sources) == 0 {
			skipped++
			continue
		}
		pages = append(pages, page)
	}
	return pages, skipped
}
