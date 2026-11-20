// migrator.go — the one-shot in-place migrator from the pre-format layout
// (structured `## key — summary` entries in MEMORY.md, corrections.md,
// multi-entry topics/<name>.md files) to the per-fact format. Copy-first:
// the old files are archived before anything is rewritten, corrections are
// folded into their target facts, stale corrections become their own facts,
// and bodies are preserved. Re-running after a partial pass converges: fact
// writes replace same-named outputs from the earlier pass.

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateResult reports what a migration converted.
type MigrateResult struct {
	Facts             int
	CorrectionsFolded int
	StaleCorrections  int
	Topics            int
}

// legacyCorrectionsName and friends pin the pre-format file names.
const legacyIndexName = "MEMORY.md"

// MigrateLegacyIfPresent converts a legacy store and reports what it did.
// A store already in the per-fact format is untouched.
func (s *Store) MigrateLegacyIfPresent() (MigrateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	legacy, err := s.scanLegacyLocked()
	if err != nil {
		return MigrateResult{}, err
	}
	if legacy.empty() {
		return MigrateResult{}, nil
	}
	result, err := s.migrateLocked(legacy)
	if err != nil {
		return MigrateResult{}, err
	}
	return result, nil
}

// legacyStore is the parsed content of a pre-format project directory.
type legacyStore struct {
	indexEntries []legacyEntry
	preamble     string
	corrections  []legacyEntry
	topics       map[string][]legacyEntry // topic name -> entries
	summary      string                   // .summary.md content, retired to the archive
}

func (l legacyStore) empty() bool {
	return len(l.indexEntries) == 0 && l.preamble == "" && len(l.corrections) == 0 && len(l.topics) == 0 && l.summary == ""
}

func (s *Store) scanLegacyLocked() (legacyStore, error) {
	var legacy legacyStore
	indexRaw, err := readMemoryFile(filepath.Join(s.projectDir, legacyIndexName))
	if err != nil {
		return legacy, err
	}
	if strings.Contains(indexRaw, "\n## ") || strings.HasPrefix(indexRaw, "## ") {
		legacy.preamble, legacy.indexEntries = parseLegacyEntries(indexRaw)
	}
	correctionsRaw, err := readMemoryFile(filepath.Join(s.projectDir, legacyCorrectionsFile))
	if err != nil {
		return legacy, err
	}
	if correctionsRaw != "" {
		_, legacy.corrections = parseLegacyEntries(correctionsRaw)
	}
	summaryRaw, err := readMemoryFile(filepath.Join(s.projectDir, legacySummaryFile))
	if err != nil {
		return legacy, err
	}
	legacy.summary = summaryRaw
	dirEntries, err := os.ReadDir(TopicsDir(s.projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return legacy, nil
		}
		return legacy, Errorf(CodeIO, "read topics: %v", err)
	}
	legacy.topics = map[string][]legacyEntry{}
	for _, entry := range dirEntries {
		if entry.IsDir() || !ValidTopicName(strings.TrimSuffix(entry.Name(), legacyTopicExt)) {
			continue
		}
		raw, err := readMemoryFile(filepath.Join(TopicsDir(s.projectDir), entry.Name()))
		if err != nil {
			return legacy, err
		}
		name := strings.TrimSuffix(entry.Name(), legacyTopicExt)
		description, entries := parseLegacyTopicFile(raw)
		if description != "" {
			for i := range entries {
				if entries[i].Summary == "" {
					entries[i].Summary = description
				}
			}
		}
		legacy.topics[name] = entries
	}
	return legacy, nil
}

func (s *Store) migrateLocked(legacy legacyStore) (MigrateResult, error) {
	result := MigrateResult{}
	// Fold corrections into their targets first; stale ones and standalone
	// corrections become facts of their own.
	byKey := map[string]*legacyEntry{}
	for i := range legacy.indexEntries {
		byKey[legacy.indexEntries[i].Key] = &legacy.indexEntries[i]
	}
	var standalone []legacyEntry
	for _, correction := range legacy.corrections {
		target, ok := byKey[correction.Corrects]
		switch {
		case correction.Corrects != "" && ok:
			target.Body = foldCorrection(target.Body, correction)
			result.CorrectionsFolded++
		default:
			correction.Key = NextKey(namesOf(legacy.indexEntries), "correction-"+correction.Key)
			standalone = append(standalone, correction)
			result.StaleCorrections++
		}
	}

	// Copy-first: archive every legacy file before anything is rewritten.
	if err := s.archiveLegacyLocked(); err != nil {
		return result, err
	}

	writeBatch := func(entries []legacyEntry, dir string) error {
		facts, err := s.listFactsLocked()
		if err != nil {
			return err
		}
		onDisk := make(map[string]Fact, len(facts))
		taken := make(map[string]bool, len(facts))
		for _, existing := range facts {
			onDisk[existing.Name] = existing
			taken[existing.Name] = true
		}
		for _, entry := range entries {
			fact, err := s.legacyFact(entry, dir)
			if err != nil {
				return err
			}
			if existing, ok := onDisk[fact.Name]; ok {
				// A partial pass may have written this entry already. Reuse
				// its name when the converted output matches; suffix only a
				// real conflict.
				if existing.Path != dir || existing.Body != fact.Body || existing.Description != fact.Description {
					fact.Name = NextKey(taken, fact.Name)
				}
			}
			if err := s.writeFactFileLocked(fact); err != nil {
				return err
			}
			taken[fact.Name] = true
			onDisk[fact.Name] = fact
			result.Facts++
		}
		return nil
	}
	if err := writeBatch(legacy.indexEntries, ""); err != nil {
		return result, err
	}
	if err := writeBatch(standalone, ""); err != nil {
		return result, err
	}
	for topic, entries := range legacy.topics {
		result.Topics++
		if err := writeBatch(entries, topicsDirName+"/"+topic); err != nil {
			return result, err
		}
	}

	// MEMORY.md is not removed here because the regenerated index overwrites it.
	for topic := range legacy.topics {
		if err := os.Remove(filepath.Join(TopicsDir(s.projectDir), topic+legacyTopicExt)); err != nil && !os.IsNotExist(err) {
			return result, Errorf(CodeIO, "remove legacy topic: %v", err)
		}
	}
	if err := s.regenerateIndexLocked(); err != nil {
		return result, err
	}
	// Retire the legacy inputs only after the converted facts and index land.
	// A failed pass keeps them, so a retry folds the same corrections.
	if err := os.Remove(filepath.Join(s.projectDir, legacyCorrectionsFile)); err != nil && !os.IsNotExist(err) {
		return result, Errorf(CodeIO, "remove legacy corrections: %v", err)
	}
	if err := os.Remove(filepath.Join(s.projectDir, legacySummaryFile)); err != nil && !os.IsNotExist(err) {
		return result, Errorf(CodeIO, "remove legacy summary: %v", err)
	}
	return result, nil
}

// archiveLegacyLocked copies the legacy files under
// archive/legacy-<timestamp>/, keeping the originals in place until the
// conversion succeeds.
func (s *Store) archiveLegacyLocked() error {
	stamp := s.now().Format("20060102T150405")
	archiveDir := filepath.Join(ArchiveDir(s.projectDir), "legacy-"+stamp)
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
			break
		}
		archiveDir = filepath.Join(ArchiveDir(s.projectDir), fmt.Sprintf("legacy-%s-%d", stamp, suffix))
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return Errorf(CodeIO, "create legacy archive: %v", err)
	}
	copyFile := func(rel string) error {
		data, err := os.ReadFile(filepath.Join(s.projectDir, rel))
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return Errorf(CodeIO, "read %s: %v", rel, err)
		}
		return os.WriteFile(filepath.Join(archiveDir, filepath.Base(rel)), data, 0o600)
	}
	for _, name := range []string{legacyIndexName, legacyCorrectionsFile, legacySummaryFile} {
		if err := copyFile(name); err != nil {
			return err
		}
	}
	topicEntries, err := os.ReadDir(TopicsDir(s.projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return Errorf(CodeIO, "read topics: %v", err)
	}
	for _, entry := range topicEntries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(TopicsDir(s.projectDir), entry.Name()))
		if err != nil {
			return Errorf(CodeIO, "read legacy topic: %v", err)
		}
		if err := os.WriteFile(filepath.Join(archiveDir, entry.Name()), data, 0o600); err != nil {
			return Errorf(CodeIO, "archive legacy topic: %v", err)
		}
	}
	return nil
}

// legacyFact converts one legacy entry into a per-format fact file write.
func (s *Store) legacyFact(entry legacyEntry, dir string) (Fact, error) {
	fact := prepareFact(Fact{
		Name:        entry.Key,
		Description: entry.Summary,
		Type:        FactTypeProject,
		Body:        entry.Body,
	})
	if ValidateFactName(fact.Name) != nil {
		fact.Name = fact.Name + "-fact"
	}
	fact.Path = dir
	return fact, nil
}

// foldCorrection appends a correction to a fact body, preserving the
// original bytes as the prefix.
func foldCorrection(body string, correction legacyEntry) string {
	var b strings.Builder
	b.WriteString(strings.Trim(body, "\n"))
	b.WriteString("\n\nCorrection: " + correction.Summary + "\n")
	if lines := strings.Trim(correction.Body, "\n"); lines != "" {
		b.WriteString(lines + "\n")
	}
	return strings.Trim(b.String(), "\n")
}

func namesOf(entries []legacyEntry) map[string]bool {
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Key] = true
	}
	return names
}

// legacyEntry is one parsed record of the pre-format entry layout.
type legacyEntry struct {
	Key      string
	Summary  string
	Corrects string
	Body     string
}

// parseLegacyEntries splits legacy content into `## key — summary` entries.
// Text before the first heading is returned as the preamble. Parsing never
// fails: the format could always be hand-edited.
func parseLegacyEntries(content string) (preamble string, entries []legacyEntry) {
	normalized := NormalizeNewlines(strings.TrimPrefix(content, "\ufeff"))
	var current *legacyEntry
	var body strings.Builder
	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.Trim(body.String(), "\n")
		entries = append(entries, *current)
		current = nil
		body.Reset()
	}
	var preambleLines []string
	for _, line := range strings.Split(normalized, "\n") {
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			key, summary := parseLegacyHeading(heading)
			current = &legacyEntry{Key: key, Summary: summary}
			continue
		}
		if current == nil {
			preambleLines = append(preambleLines, line)
			continue
		}
		if isLegacyMetaLine(line) {
			applyLegacyMeta(current, line)
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	preamble = strings.TrimSpace(strings.Join(preambleLines, "\n"))
	return preamble, entries
}

// parseLegacyTopicFile splits a legacy topic file into its `# description`
// heading and its entries.
func parseLegacyTopicFile(content string) (description string, entries []legacyEntry) {
	normalized := NormalizeNewlines(strings.TrimPrefix(content, "\ufeff"))
	rest := normalized
	if heading, tail, ok := strings.Cut(normalized, "\n"); ok && strings.HasPrefix(heading, "# ") {
		description = strings.TrimSpace(strings.TrimPrefix(heading, "# "))
		rest = tail
	} else if strings.HasPrefix(normalized, "# ") {
		description = strings.TrimSpace(strings.TrimPrefix(normalized, "# "))
		rest = ""
	}
	_, entries = parseLegacyEntries(rest)
	return description, entries
}

// parseLegacyHeading splits "key — summary" (also accepting " - ").
func parseLegacyHeading(text string) (key, summary string) {
	collapsed := CollapseLine(text)
	for _, separator := range []string{" — ", " - "} {
		if keyPart, rest, ok := strings.Cut(collapsed, separator); ok {
			if normalized := FactSlug(keyPart); normalized != "" {
				return normalized, rest
			}
		}
	}
	if slug := FactSlug(collapsed); slug != "" {
		return slug, ""
	}
	return "entry", collapsed
}

// isLegacyMetaLine reports whether a line is one of the legacy metadata
// lines rather than body text.
func isLegacyMetaLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range []string{"type:", "files:", "corrects:", "corrected-by:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// applyLegacyMeta consumes one legacy metadata line.
func applyLegacyMeta(entry *legacyEntry, line string) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "type:"):
		// Legacy types (fact, environment, note) all map to project.
	case strings.HasPrefix(lower, "corrects:"):
		entry.Corrects = FactSlug(strings.TrimSpace(trimmed[len("corrects:"):]))
	}
}
