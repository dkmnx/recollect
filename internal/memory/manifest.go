// manifest.go — the capture-time file inventory. The consolidation pass sees
// which memory files already exist (name, kind, description, mtime) with a
// short content preview, so it updates an existing fact instead of writing a
// near-duplicate. Best-effort by design: the capture prompt degrades to the
// index hint when the inventory cannot be built.

package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manifest kinds label one memory file in the inventory.
const (
	ManifestKindIndex  = "index"
	ManifestKindFact   = "fact"
	ManifestKindDigest = "digest"
)

// ManifestEntry is one memory file described for the capture prompt.
type ManifestEntry struct {
	Name        string
	Kind        string
	Description string
	ModTime     time.Time
	Preview     string
}

// Manifest builds the project memory file inventory. Durable knowledge files
// come first, newest first within each kind, so the render byte budget always
// covers the update-not-duplicate targets before session history. A file that
// is not a regular file (a planted symlink or a directory) is skipped; a
// genuine read failure is returned.
func (s *Store) Manifest() ([]ManifestEntry, error) {
	limits := s.Limits()
	var entries []ManifestEntry

	add := func(rel, kind, name string, parse func(string) (string, string)) error {
		path := filepath.Join(s.projectDir, rel)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return Errorf(CodeIO, "stat %s: %v", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		content, err := readMemoryFile(path)
		if err != nil {
			return err
		}
		description, preview := parse(content)
		entries = append(entries, ManifestEntry{
			Name:        name,
			Kind:        kind,
			Description: description,
			ModTime:     info.ModTime(),
			Preview:     preview,
		})
		return nil
	}

	// Everything in the inventory leaves the process for the capture
	// provider, so it is redacted like the transcript: a secret the writer
	// missed must not ride along in a description or preview.
	describeFact := func(content string) (string, string) {
		fact := ParseFact(content)
		return Redact(fact.Description), manifestPreview(Redact(fact.Body), limits.ManifestPreviewLines)
	}
	describeDigest := func(content string) (string, string) {
		if digest, ok := ParseDigest(content, ""); ok {
			return Redact(CollapseLine(digest.Topic)), manifestPreview(Redact(digest.Summary), limits.ManifestPreviewLines)
		}
		return "", ""
	}

	if err := add(indexFileName, ManifestKindIndex, "MEMORY.md", func(content string) (string, string) {
		return fmt.Sprintf("index of %d facts", strings.Count(content, "- [")), manifestPreview(Redact(content), limits.ManifestPreviewLines)
	}); err != nil {
		return nil, err
	}
	dirEntries, err := os.ReadDir(s.projectDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, Errorf(CodeIO, "read project dir: %v", err)
	}
	for _, entry := range dirEntries {
		if entry.IsDir() || entry.Name() == indexFileName || !strings.HasSuffix(entry.Name(), markdownExt) {
			continue
		}
		if err := add(entry.Name(), ManifestKindFact, strings.TrimSuffix(entry.Name(), markdownExt), describeFact); err != nil {
			return nil, err
		}
	}
	topicEntries, err := os.ReadDir(TopicsDir(s.projectDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, Errorf(CodeIO, "read topics: %v", err)
	}
	for _, topic := range topicEntries {
		if !topic.IsDir() || !ValidTopicName(topic.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(TopicsDir(s.projectDir), topic.Name()))
		if err != nil {
			return nil, Errorf(CodeIO, "read topic %s: %v", topic.Name(), err)
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), markdownExt) {
				continue
			}
			rel := filepath.Join(topicsDirName, topic.Name(), file.Name())
			name := topicsDirName + "/" + topic.Name() + "/" + strings.TrimSuffix(file.Name(), markdownExt)
			if err := add(rel, ManifestKindFact, name, describeFact); err != nil {
				return nil, err
			}
		}
	}
	digestNames, err := s.digestNames()
	if err != nil {
		return nil, err
	}
	for _, name := range digestNames {
		rel := filepath.Join(sessionsDirName, name)
		if err := add(rel, ManifestKindDigest, name, describeDigest); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if rank := manifestKindRank(left.Kind) - manifestKindRank(right.Kind); rank != 0 {
			return rank < 0
		}
		if !left.ModTime.Equal(right.ModTime) {
			return left.ModTime.After(right.ModTime)
		}
		return left.Name < right.Name
	})
	if len(entries) > limits.ManifestMaxFiles {
		entries = entries[:limits.ManifestMaxFiles]
	}
	return entries, nil
}

// manifestKindRank orders durable knowledge files ahead of session digests.
// The renderer fills a byte budget in order, and freshly rewritten digests are
// always the newest files; without this the budget would spend itself on
// session history and starve the facts that are the actual
// update-not-duplicate targets.
func manifestKindRank(kind string) int {
	switch kind {
	case ManifestKindIndex:
		return 0
	case ManifestKindFact:
		return 1
	default:
		return 2
	}
}

// digestNames lists digest file names.
func (s *Store) digestNames() ([]string, error) {
	dirEntries, err := os.ReadDir(SessionsDir(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Errorf(CodeIO, "read digests: %v", err)
	}
	names := make([]string, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), digestFileExt) {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

// manifestPreview returns the first max non-empty lines of a fact body or
// digest summary.
func manifestPreview(content string, max int) string {
	if max <= 0 {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(NormalizeNewlines(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= max {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// ManifestBlock returns the rendered inventory for the capture prompt, empty
// when the project has no memory files.
func (s *Store) ManifestBlock() (string, error) {
	entries, err := s.Manifest()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return RenderManifest(entries, s.Limits().ManifestMaxBytes), nil
}

// RenderManifest renders the inventory within maxBytes, newest first. Files
// that do not fit are counted in a trailing marker.
func RenderManifest(entries []ManifestEntry, maxBytes int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<project_memory_files count=%d>\n", len(entries))
	rendered := 0
	for _, entry := range entries {
		block := renderManifestEntry(entry)
		if maxBytes > 0 && b.Len()+len(block)+len("</project_memory_files>\n") > maxBytes {
			break
		}
		b.WriteString(block)
		rendered++
	}
	if omitted := len(entries) - rendered; omitted > 0 {
		fmt.Fprintf(&b, "[%d more files]\n", omitted)
	}
	b.WriteString("</project_memory_files>")
	return b.String()
}

// renderManifestEntry renders one file header plus its indented preview.
func renderManifestEntry(entry ManifestEntry) string {
	header := entry.Name + " [" + entry.Kind + "]"
	if !entry.ModTime.IsZero() {
		header += " " + entry.ModTime.Format("2006-01-02")
	}
	if entry.Description != "" {
		header += ": " + entry.Description
	}
	if entry.Preview == "" {
		return header + "\n"
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for _, line := range strings.Split(entry.Preview, "\n") {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}
