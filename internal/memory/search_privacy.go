package memory

import (
	"path/filepath"
	"strings"
	"time"
)

// searchFact is one fact's searchable projection plus its unfiltered identity.
type searchFact struct {
	Fact
	RelPath    string
	SourceHash string
	terms      factTerms // Immutable after preparation, including across searches.
}

// stripPrivateForSearch removes private spans without leaving a searchable marker.
// Newlines survive so frontmatter and field boundaries stay in place.
func stripPrivateForSearch(content string) string {
	if !strings.Contains(content, "<") {
		return content
	}
	matches := privateTagPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return content
	}
	var b strings.Builder
	mask := func(text string) {
		for _, r := range text {
			if r == '\n' || r == '\r' {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
	}
	depth, cursor := 0, 0
	for _, loc := range matches {
		if content[loc[2]:loc[3]] != "/" {
			if depth == 0 {
				b.WriteString(content[cursor:loc[0]])
			}
			depth++
			cursor = loc[1]
			continue
		}
		if depth == 0 {
			b.WriteString(content[cursor:loc[0]])
			cursor = loc[1]
			continue
		}
		depth--
		if depth == 0 {
			mask(content[cursor:loc[0]])
			cursor = loc[1]
		}
	}
	if depth > 0 {
		mask(content[cursor:])
	} else {
		b.WriteString(content[cursor:])
	}
	return b.String()
}

// secretLiterals are conservative lead-ins for every secret pattern. A text
// without one cannot match, so the regex pass is skipped.
var secretLiterals = func() []string {
	literals := make([]string, 0, len(secretLeads)+len(secretNames)+3)
	for _, lead := range secretLeads {
		literals = append(literals, lead.lead)
	}
	literals = append(literals, secretNames...)
	return append(literals, "-----begin", "eyj", "://")
}()

func removeSecretsForSearch(text string) string {
	lower := strings.ToLower(text)
	for _, literal := range secretLiterals {
		if strings.Contains(lower, literal) {
			for _, pattern := range secretPatterns {
				text = pattern.ReplaceAllString(text, " ")
			}
			return text
		}
	}
	return text
}

func filterSearchText(text string) string {
	return removeSecretsForSearch(stripPrivateForSearch(text))
}

func filteredFact(rel string, raw string, mod time.Time, hash string) (searchFact, bool) {
	masked := stripPrivateForSearch(raw)
	fact := parseFactBytes(rel, masked, mod)
	base := strings.TrimSuffix(filepath.Base(rel), markdownExt)
	if fact.Name == FactSlug(base) {
		fact.Name = FactSlug(filterSearchText(base))
	}
	if fact.Description == "" {
		fact.Description = CollapseLine(firstNonEmptyLine(fact.Body))
	}
	fact.Name = removeSecretsForSearch(fact.Name)
	fact.Description = removeSecretsForSearch(fact.Description)
	fact.Body = removeSecretsForSearch(fact.Body)
	fact.Scene = removeSecretsForSearch(fact.Scene)
	if strings.TrimSpace(fact.Name) == "" || strings.TrimSpace(fact.Description) == "" &&
		strings.TrimSpace(fact.Body) == "" {
		return searchFact{}, false
	}
	// Keep only the search fields and detach them from the source buffer.
	projection := Fact{
		Name: strings.Clone(fact.Name), Description: strings.Clone(fact.Description),
		Body: strings.Clone(fact.Body), Scene: strings.Clone(fact.Scene),
		Links: fact.Links, Path: fact.Path, ModTime: fact.ModTime,
	}
	return searchFact{
		Fact:       projection,
		RelPath:    filepath.ToSlash(rel),
		SourceHash: hash,
		terms:      prepareFactTerms(projection),
	}, true
}

func (s *Store) searchCorpus() ([]searchFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.listFactFilesLocked()
	if err != nil {
		return nil, err
	}
	out := make([]searchFact, 0, len(files))
	next := make(map[string]searchFact, len(files))
	for _, file := range files {
		hash := factContentHash(file.rel, []byte(file.raw))
		fact, ok := s.searchCache[file.rel]
		if !ok || fact.SourceHash != hash {
			fact, ok = filteredFact(file.rel, file.raw, file.mod, hash)
			if !ok {
				continue
			}
		}
		fact.ModTime = file.mod
		next[file.rel] = fact
		out = append(out, fact)
	}
	s.searchCache = next
	return out, nil
}
