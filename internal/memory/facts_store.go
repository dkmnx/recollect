// facts_store.go — fact operations on the store: one fact per markdown
// file, MEMORY.md regenerated from the files on every mutation, and no size
// refusal anywhere (ADR-005). Every mutation is one locked critical
// section, so tool and capture writers serialize on the store mutex alone.

package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// IndexPath is the project index (MEMORY.md) path.
func (s *Store) IndexPath() string { return IndexFile(s.projectDir) }

// ReadIndex returns the raw MEMORY.md content, or "" when absent.
func (s *Store) ReadIndex() (string, error) { return readMemoryFile(s.IndexPath()) }

// Ping reports whether the project directory is writable, so capture
// parking can distinguish "failed lately" from "cannot ever land a write".
func (s *Store) Ping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	probe, err := containPath(s.projectDir, ".ping")
	if err != nil {
		return err
	}
	if err := s.writeLocked(probe, ""); err != nil {
		return err
	}
	if err := os.Remove(probe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Errorf(CodeIO, "remove probe: %v", err)
	}
	return nil
}

// ListFacts walks the project directory and parses every markdown file
// except the index: root files are root facts, topics/<topic>/*.md are
// topic facts, and stray files or directories are skipped so hand-placed
// content can never break the walk. Newest first, ties by name — the index
// order.
func (s *Store) ListFacts() ([]Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listFactsLocked()
}

type factFile struct {
	rel string
	raw string
	mod time.Time
}

func (s *Store) listFactFilesLocked() ([]factFile, error) {
	var files []factFile
	add := func(rel string) error {
		path := filepath.Join(s.projectDir, rel)
		raw, err := readMemoryFile(path)
		if err != nil {
			return err
		}
		var mod time.Time
		if info, err := os.Lstat(path); err == nil {
			mod = info.ModTime()
		}
		files = append(files, factFile{rel: rel, raw: raw, mod: mod})
		return nil
	}
	dirEntries, err := os.ReadDir(s.projectDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, Errorf(CodeIO, "read project dir: %v", err)
	}
	for _, entry := range dirEntries {
		if entry.IsDir() || reservedFactFile(entry.Name()) || !strings.HasSuffix(entry.Name(), markdownExt) {
			continue
		}
		if err := add(entry.Name()); err != nil {
			return nil, err
		}
	}
	topicsDir := TopicsDir(s.projectDir)
	if info, err := os.Lstat(topicsDir); err == nil && !info.IsDir() {
		return nil, Errorf(CodeIO, "topics path is not a directory: %s", topicsDir)
	}
	topicEntries, err := os.ReadDir(topicsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, Errorf(CodeIO, "read topics: %v", err)
	}
	for _, topic := range topicEntries {
		if !topic.IsDir() || !ValidTopicName(topic.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(TopicsDir(s.projectDir), topic.Name()))
		if err != nil {
			return nil, Errorf(CodeIO, "read topic %s: %v", topic.Name(), err)
		}
		for _, file := range entries {
			if file.IsDir() || !strings.HasSuffix(file.Name(), markdownExt) {
				continue
			}
			if err := add(filepath.Join(topicsDirName, topic.Name(), file.Name())); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// reservedFactFile reports a Markdown file the store owns. Case-insensitive
// because MEMORY.md and memory.md collide on common desktop filesystems.
func reservedFactFile(name string) bool {
	switch strings.ToLower(name) {
	case "memory.md", "corrections.md", ".summary.md":
		return true
	}
	return false
}

func parseFactBytes(rel string, raw string, mod time.Time) Fact {
	fact := ParseFact(raw)
	base := strings.TrimSuffix(filepath.Base(rel), markdownExt)
	if fact.Name == "" {
		fact.Name = FactSlug(base)
	}
	if fact.Name == "" {
		fact.Name = base
	}
	if fact.Description == "" {
		fact.Description = CollapseLine(firstNonEmptyLine(fact.Body))
	}
	fact.Path = filepath.ToSlash(filepath.Dir(rel))
	if fact.Path == "." {
		fact.Path = ""
	}
	fact.ModTime = mod
	fact.relPath = filepath.ToSlash(rel)
	return fact
}

func (s *Store) listFactsLocked() ([]Fact, error) {
	files, err := s.listFactFilesLocked()
	if err != nil {
		return nil, err
	}
	facts := make([]Fact, 0, len(files))
	for _, file := range files {
		facts = append(facts, parseFactBytes(file.rel, file.raw, file.mod))
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if !facts[i].ModTime.Equal(facts[j].ModTime) {
			return facts[i].ModTime.After(facts[j].ModTime)
		}
		return facts[i].Name < facts[j].Name
	})
	return facts, nil
}

// CountFacts counts the facts in a project-shaped directory with the same
// file selection listFactsLocked applies (top-level .md files plus
// ValidTopicName topic subdirectories, stray files skipped). Surfaces that
// only need the count — /memory status over agent directories — use this
// instead of keeping a second enumeration rule.
func CountFacts(projectDir string) (int, error) {
	store := &Store{projectDir: projectDir}
	facts, err := store.listFactsLocked()
	return len(facts), err
}

// ReadFact returns the fact with the given name, searching the root first
// and then the topic directories.
func (s *Store) ReadFact(name string) (Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return Fact{}, err
	}
	if fact == nil {
		return Fact{}, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	return *fact, nil
}

// ReadFactWithBytes returns a fact and the exact bytes it was parsed from.
// Enumeration applies the same file checks as ReadFact, and parseFactBytes
// supplies the fallback name and description for facts without frontmatter.
func (s *Store) ReadFactWithBytes(name string) (Fact, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return Fact{}, nil, err
	}
	if fact == nil {
		return Fact{}, nil, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	rel := fact.RelPath()
	raw, err := readMemoryFile(filepath.Join(s.projectDir, filepath.FromSlash(rel)))
	if err != nil {
		return Fact{}, nil, err
	}
	return parseFactBytes(rel, raw, fact.ModTime), []byte(raw), nil
}

// AppendFact writes a new fact, never overwriting: a name taken anywhere in
// the project gets a numeric suffix, and the fact actually written (with
// its resolved name) is returned. An empty Path creates at the root;
// "topics/<topic>" creates inside a topic directory.
func (s *Store) AppendFact(fact Fact) (Fact, error) {
	prepared, err := prepareNewFact(fact)
	if err != nil {
		return Fact{}, err
	}
	if (strings.TrimSpace(prepared.Description) != "" && IsFullyPrivate(prepared.Description)) ||
		(strings.TrimSpace(prepared.Body) != "" && IsFullyPrivate(prepared.Body)) {
		return Fact{}, Errorf(CodePrivateRejected, "memory content is fully private")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prepared.CapturedOn == "" {
		prepared.CapturedOn = s.now().Format(time.DateOnly)
	}
	facts, err := s.listFactsLocked()
	if err != nil {
		return Fact{}, err
	}
	names := make(map[string]bool, len(facts))
	for i := range facts {
		names[facts[i].Name] = true
	}
	if duplicate := findDuplicateBody(facts, prepared.Body); duplicate != "" {
		return Fact{}, Errorf(CodeDuplicate, "identical fact already exists as %q", duplicate)
	}
	prepared.Name = NextKey(names, prepared.Name)
	if err := s.writeFactLocked(prepared); err != nil {
		return Fact{}, err
	}
	s.journalLocked(journalEntry("append", prepared.Name, "", prepared.Description, prepared.OriginSessionID))
	return prepared, nil
}

// UpsertFact writes fact, replacing an existing fact with the same name in
// place (at its current location) and creating it otherwise. Capture's
// add and update ops converge through here. Replacing keeps the existing
// CapturedOn: a correction does not move the date the knowledge was learned.
// A body the store already holds under any name is a no-op that returns the
// existing fact, so a retried or re-derived extraction cannot duplicate
// knowledge or refresh a file it did not change.
func (s *Store) UpsertFact(fact Fact) (Fact, error) {
	prepared, err := prepareNewFact(fact)
	if err != nil {
		return Fact{}, err
	}
	if (strings.TrimSpace(prepared.Description) != "" && IsFullyPrivate(prepared.Description)) ||
		(strings.TrimSpace(prepared.Body) != "" && IsFullyPrivate(prepared.Body)) {
		return Fact{}, Errorf(CodePrivateRejected, "memory content is fully private")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	facts, err := s.listFactsLocked()
	if err != nil {
		return Fact{}, err
	}
	if duplicate := findDuplicateBody(facts, prepared.Body); duplicate != "" {
		if match := findFactIn(facts, duplicate); match != nil {
			// Knowledge already lives at the matching fact: a different name
			// never rewrites it. The same fact skips the write only when
			// nothing changed, so a description or links refinement still
			// lands without refreshing the date the fact was learned.
			if match.Name != prepared.Name {
				return *match, nil
			}
			if match.Description == prepared.Description && match.Scene == prepared.Scene &&
				match.Type == prepared.Type && equalLinks(match.Links, prepared.Links) {
				return *match, nil
			}
		}
	}
	existing := findFactIn(facts, prepared.Name)
	if existing != nil {
		prepared.Path = existing.Path
		prepared.relPath = existing.RelPath()
		prepared.CapturedOn = existing.CapturedOn
	} else if prepared.CapturedOn == "" {
		prepared.CapturedOn = s.now().Format(time.DateOnly)
	}
	if err := s.writeFactLocked(prepared); err != nil {
		return Fact{}, err
	}
	oldDescription := ""
	if existing != nil {
		oldDescription = existing.Description
	}
	s.journalLocked(journalEntry("upsert", prepared.Name, oldDescription, prepared.Description, prepared.OriginSessionID))
	return prepared, nil
}

// UpdateFact corrects a fact in place: a non-empty description replaces the
// old one and non-empty body content replaces the body. A nil links slice
// keeps the existing links; a non-nil one replaces them (normalized). The
// fact must exist; nothing is ever refused for size.
func (s *Store) UpdateFact(name, description, body string, links []string) (Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return Fact{}, err
	}
	if existing == nil {
		return Fact{}, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	updated := *existing
	if collapsed := CollapseLine(description); collapsed != "" {
		collapsed = StripPrivateContent(collapsed)
		if IsFullyPrivate(collapsed) {
			return Fact{}, Errorf(CodePrivateRejected, "memory content is fully private")
		}
		updated.Description = collapsed
	}
	if strings.TrimSpace(body) != "" {
		body = StripPrivateContent(strings.Trim(body, "\n"))
		if IsFullyPrivate(body) {
			return Fact{}, Errorf(CodePrivateRejected, "memory content is fully private")
		}
		updated.Body = body
	}
	if links != nil {
		updated.Links = normalizeLinks(updated.Name, links)
	}
	if err := s.writeFactLocked(updated); err != nil {
		return Fact{}, err
	}
	s.journalLocked(journalEntry("update", updated.Name, existing.Description, updated.Description, updated.OriginSessionID))
	return updated, nil
}

// EditFact replaces oldString in a fact's body, returning the number of
// replacements. Replacement is literal.
func (s *Store) EditFact(name, oldString, newString string, replaceAll bool) (Fact, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return Fact{}, 0, err
	}
	if existing == nil {
		return Fact{}, 0, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	newString = StripPrivateContent(newString)
	if strings.TrimSpace(newString) != "" && IsFullyPrivate(newString) {
		return Fact{}, 0, Errorf(CodePrivateRejected, "memory content is fully private")
	}
	edited, replacements, err := ApplyEdit(existing.Body, oldString, newString, replaceAll)
	if err != nil {
		return Fact{}, 0, err
	}
	updated := *existing
	updated.Body = edited
	if err := s.writeFactLocked(updated); err != nil {
		return Fact{}, 0, err
	}
	s.journalLocked(journalEntry("edit", updated.Name, "", "", updated.OriginSessionID))
	return updated, replacements, nil
}

// MoveFactToTopic moves a fact file into topics/<topic>/ — the demote
// operation. Moving into the fact's current topic is a no-op; a name
// collision inside the target gets a numeric suffix. The moved fact is
// returned.
func (s *Store) MoveFactToTopic(name, topic string) (Fact, error) {
	if !ValidTopicName(topic) {
		return Fact{}, &Error{Code: CodeInvalidTopic, Message: "invalid topic name: " + topic}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return Fact{}, err
	}
	if existing == nil {
		return Fact{}, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	if existing.Path == "topics/"+topic {
		return *existing, nil
	}
	moved := *existing
	moved.relPath = ""
	moved.Path = topicsDirName + "/" + topic
	taken, err := s.topicFactNamesLocked(topic)
	if err != nil {
		return Fact{}, err
	}
	moved.Name = NextKey(taken, moved.Name)
	if err := s.writeFactLocked(moved); err != nil {
		return Fact{}, err
	}
	if err := os.Remove(filepath.Join(s.projectDir, existing.RelPath())); err != nil {
		return Fact{}, Errorf(CodeIO, "remove moved fact: %v", err)
	}
	if err := s.regenerateIndexLocked(); err != nil {
		return Fact{}, err
	}
	s.journalLocked(journalEntry("demote", moved.Name, "", "", moved.OriginSessionID))
	return moved, nil
}

// DeleteFact removes the fact file with the given name and reports whether
// it existed. A missing fact is not an error: a drop is maintenance, and
// the end state it asks for already holds.
func (s *Store) DeleteFact(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, nil
	}
	if err := os.Remove(filepath.Join(s.projectDir, existing.RelPath())); err != nil {
		return false, Errorf(CodeIO, "delete fact: %v", err)
	}
	if err := s.regenerateIndexLocked(); err != nil {
		return false, err
	}
	s.journalLocked(journalEntry("drop", existing.Name, existing.Description, "", existing.OriginSessionID))
	return true, nil
}

// DeleteTopic removes a topic directory with every fact inside it and
// reports whether it existed. A missing topic is not an error and records
// nothing: the end state it asks for already holds.
func (s *Store) DeleteTopic(topic string) (bool, error) {
	if !ValidTopicName(topic) {
		return false, &Error{Code: CodeInvalidTopic, Message: "invalid topic name: " + topic}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(TopicsDir(s.projectDir), topic)
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, Errorf(CodeIO, "stat topic: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, Errorf(CodeIO, "delete topic: %v", err)
	}
	if err := s.regenerateIndexLocked(); err != nil {
		return false, err
	}
	s.journalLocked(journalEntry("drop-topic", topic, "", "", ""))
	return true, nil
}

// TopicDirs lists the addressable topic directory names.
func (s *Store) TopicDirs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.topicDirsLocked()
}

func (s *Store) topicDirsLocked() ([]string, error) {
	dirEntries, err := os.ReadDir(TopicsDir(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Errorf(CodeIO, "read topics: %v", err)
	}
	var names []string
	for _, entry := range dirEntries {
		if entry.IsDir() && ValidTopicName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// RegenerateIndex rewrites MEMORY.md from the fact files. /memory rebuild
// and the migrator use it to re-derive the index after hand edits or
// conversion.
func (s *Store) RegenerateIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regenerateIndexLocked()
}

// IndexBytes reports the index file's size on disk, for the capture
// fullness hint and /memory status.
func (s *Store) IndexBytes() (int, error) {
	raw, err := readMemoryFile(s.IndexPath())
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}

// findFactLocked resolves a fact name to its file, root first then topics.
func (s *Store) findFactLocked(name string) (*Fact, error) {
	facts, err := s.listFactsLocked()
	if err != nil {
		return nil, err
	}
	return findFactIn(facts, name), nil
}

// findFactIn resolves a name inside an already-listed set, root first then
// topics.
func findFactIn(facts []Fact, name string) *Fact {
	var match *Fact
	for i := range facts {
		if facts[i].Name == name {
			match = &facts[i]
			if facts[i].Path == "" {
				break // root wins over a same-named topic fact
			}
		}
	}
	return match
}

// topicFactNamesLocked maps the fact names inside one topic.
func (s *Store) topicFactNamesLocked(topic string) (map[string]bool, error) {
	files, err := os.ReadDir(filepath.Join(TopicsDir(s.projectDir), topic))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, Errorf(CodeIO, "read topic %s: %v", topic, err)
	}
	names := map[string]bool{}
	for _, file := range files {
		if !file.IsDir() {
			names[strings.TrimSuffix(file.Name(), markdownExt)] = true
		}
	}
	return names, nil
}

// writeFactLocked renders and writes one fact file, then regenerates the
// index. Callers hold the lock.
func (s *Store) writeFactLocked(fact Fact) error {
	if err := s.writeFactFileLocked(fact); err != nil {
		return err
	}
	return s.regenerateIndexLocked()
}

// writeFactFileLocked writes one fact file without touching the index.
// Migration batches use it so a mid-batch failure leaves the legacy index in
// place for a retry. Callers hold the lock.
func (s *Store) writeFactFileLocked(fact Fact) error {
	if err := ValidateFactName(fact.Name); err != nil {
		return err
	}
	rel := fact.RelPath()
	target := filepath.Join(s.projectDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Errorf(CodeIO, "create %s: %v", filepath.Dir(target), err)
	}
	return s.writeLocked(target, FormatFact(fact))
}

// regenerateIndexLocked rewrites MEMORY.md from the fact files and takes
// the daily snapshot. Callers hold the lock.
func (s *Store) regenerateIndexLocked() error {
	facts, err := s.listFactsLocked()
	if err != nil {
		return err
	}
	if _, err := s.ensureProjectLocked(); err != nil {
		return err
	}
	if err := s.writeLocked(IndexFile(s.projectDir), RenderIndex(facts)); err != nil {
		return err
	}
	// Best effort: the index already landed, and a snapshot failure must
	// not fail the operation.
	_, _, _ = s.snapshotMainLocked()
	return nil
}

// prepareNewFact validates a fact for a create-or-replace write.
func prepareNewFact(fact Fact) (Fact, error) {
	prepared := prepareFact(fact)
	prepared.relPath = ""
	if prepared.Description == "" {
		return Fact{}, &Error{Code: CodeMissingField, Message: "description is required"}
	}
	if err := ValidateFactPath(fact.Path); err != nil {
		return Fact{}, err
	}
	if ValidateFactName(prepared.Name) != nil {
		return Fact{}, Errorf(CodeInvalidTopic, "reserved fact name: %s", prepared.Name)
	}
	return prepared, nil
}

// ValidateFactPath accepts an empty root path or a valid topics/<name> path.
func ValidateFactPath(path string) error {
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, topicsDirName+"/") {
		return &Error{Code: CodeInvalidTopic, Message: "invalid topic path: " + path}
	}
	if !ValidTopicName(strings.TrimPrefix(path, topicsDirName+"/")) {
		return &Error{Code: CodeInvalidTopic, Message: "invalid topic path: " + path}
	}
	return nil
}

// ValidateFactName rejects names whose files collide with store-owned files.
func ValidateFactName(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "memory", "corrections":
		return Errorf(CodeInvalidTopic, "reserved fact name: %s", name)
	}
	return nil
}

// firstNonEmptyLine returns the first non-empty line.
func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
