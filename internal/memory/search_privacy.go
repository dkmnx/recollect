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
}

// stripPrivateForSearch removes private spans without leaving a searchable marker.
// Newlines survive so frontmatter and field boundaries stay in place.
func stripPrivateForSearch(content string) string {
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

func removeSecretsForSearch(text string) string {
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, " ")
	}
	return text
}

func filterSearchText(text string) string {
	return removeSecretsForSearch(stripPrivateForSearch(text))
}

func filteredFact(rel string, raw string, mod time.Time) (searchFact, bool) {
	masked := stripPrivateForSearch(raw)
	fact := parseFactBytes(rel, masked, mod)
	base := strings.TrimSuffix(filepath.Base(rel), markdownExt)
	if fact.Name == FactSlug(base) {
		fact.Name = FactSlug(filterSearchText(base))
	}
	if fact.Description == "" {
		fact.Description = CollapseLine(firstNonEmptyLine(fact.Body))
	}
	fact.Name = filterSearchText(fact.Name)
	fact.Description = filterSearchText(fact.Description)
	fact.Body = filterSearchText(fact.Body)
	fact.Scene = filterSearchText(fact.Scene)
	if strings.TrimSpace(fact.Name) == "" || strings.TrimSpace(fact.Description) == "" &&
		strings.TrimSpace(fact.Body) == "" {
		return searchFact{}, false
	}
	return searchFact{
		Fact:       fact,
		RelPath:    filepath.ToSlash(rel),
		SourceHash: factContentHash(rel, []byte(raw)),
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
	for _, file := range files {
		if fact, ok := filteredFact(file.rel, file.raw, file.mod); ok {
			out = append(out, fact)
		}
	}
	return out, nil
}
