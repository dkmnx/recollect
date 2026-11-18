// chain.go — the revision chain: every applied file edit recorded as one
// append-only line, so recall can attach a file's real change trajectory
// (including reverted versions no fact captured) with zero LLM calls. The
// chain is a decorator, not a retriever: facts stay the recall surface,
// and the chain only attaches revisions under what a caller already reads.

package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// chainRotateBytes rotates chains.jsonl to chains.prev.jsonl. Revisions
// carry diff text, so the bound sits above the journal's.
const chainRotateBytes = 2 << 20

// chainMaxPaths bounds the distinct-path list a decoration sweep walks.
const chainMaxPaths = 256

// ChainRevision is one applied edit: the file, the session that applied
// it, and the replaced text pair exactly as the tool call carried them.
type ChainRevision struct {
	Path      string    `json:"path"`
	SessionID string    `json:"sessionID,omitempty"`
	At        time.Time `json:"at"`
	OldText   string    `json:"oldText,omitempty"`
	NewText   string    `json:"newText"`
}

// AppendChainRevisions appends revisions in order, rotating at the bound.
// The whole batch lands through one write, so a failure leaves no partial
// batch for a retry to duplicate; the caller turns an error into a
// warning, never a failed phase.
func (s *Store) AppendChainRevisions(revisions []ChainRevision) error {
	if len(revisions) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := ChainFile(s.projectDir)
	if info, err := os.Lstat(path); err == nil && info.Size() >= chainRotateBytes {
		prev := filepath.Join(s.projectDir, chainPrevFile)
		_ = os.Remove(prev)
		_ = os.Rename(path, prev)
	}
	current, err := readMemoryFile(path)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(current)
	for _, revision := range revisions {
		if revision.At.IsZero() {
			revision.At = s.now()
		}
		revision.Path = strings.TrimSpace(revision.Path)
		line, err := json.Marshal(revision)
		if err != nil {
			return err
		}
		b.WriteString(string(line) + "\n")
	}
	return s.writeLocked(path, b.String())
}

// ChainRevisions returns one path's revisions in write order, capped at
// the newest max (non-positive max means no cap).
func (s *Store) ChainRevisions(path string, max int) ([]ChainRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(ChainFile(s.projectDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var revisions []ChainRevision
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var revision ChainRevision
		if json.Unmarshal([]byte(line), &revision) != nil {
			continue
		}
		if revision.Path != path {
			continue
		}
		revisions = append(revisions, revision)
	}
	if max > 0 && len(revisions) > max {
		revisions = revisions[len(revisions)-max:]
	}
	return revisions, nil
}

// ChainPaths lists the chain's distinct paths, sorted, capped at
// chainMaxPaths. A decoration sweep walks this list, so the cap keeps the
// sweep bounded on a store that touched many files.
func (s *Store) ChainPaths() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(ChainFile(s.projectDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var revision ChainRevision
		if json.Unmarshal([]byte(line), &revision) != nil {
			continue
		}
		if revision.Path == "" || seen[revision.Path] {
			continue
		}
		seen[revision.Path] = true
		paths = append(paths, revision.Path)
		if len(paths) == chainMaxPaths {
			break
		}
	}
	sort.Strings(paths)
	return paths, nil
}
