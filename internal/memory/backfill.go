// backfill.go — git-history backfill bookkeeping for the revision chain:
// fact reads record workspace files the chain has never seen as pending,
// and the capture pass drains the pending set. The marker map lives in
// CaptureState; this file owns the candidate rule and the mark path.

package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// chainBackfillMaxEntries bounds the marker map in capture.json: a store
// that names many files over its lifetime cannot grow the state file
// without bound. shortcut: once full, new candidates are refused until
// entries drain; revisit if a project ever reaches the bound in practice.
const chainBackfillMaxEntries = 256

// chainPathTokenRE matches path-like tokens: runs of path characters
// ending in a short extension. The class carries the colon so a Windows
// drive letter stays inside the token, and prose tokens ("e.g") survive
// the match and die on the stat check.
// The class keeps ~ so 8.3 short names (RUNNER~1) survive tokenization; a
// truncated absolute path turns relative and drops, losing the candidate.
var chainPathTokenRE = regexp.MustCompile(`[A-Za-z0-9_./\\~:-]+\.[A-Za-z0-9]{1,10}`)

// ChainBackfillCandidates extracts up to max workspace files the text
// names: path-like tokens that stat as regular files under root, spelled
// in the chain's root-relative identity. Out-of-root absolutes and
// directories drop.
func ChainBackfillCandidates(text, root string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" || root == "" || max <= 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, token := range chainPathTokenRE.FindAllString(text, -1) {
		token = strings.Trim(token, ".,:;)")
		if len(token) < 3 || seen[token] {
			continue
		}
		seen[token] = true
		rel := chainTokenIdentity(token, root)
		if rel == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, rel)
		if len(out) == max {
			break
		}
	}
	return out
}

// chainTokenIdentity normalizes a fact-named token the way an edit path
// normalizes: a relative token stays as typed (slash-separated), an
// absolute path under root becomes root-relative, and anything else drops,
// because backfill scrapes only repo files.
func chainTokenIdentity(token, root string) string {
	token = filepath.ToSlash(strings.TrimSpace(token))
	if token == "" {
		return ""
	}
	if !filepath.IsAbs(token) {
		return token
	}
	if rel, err := filepath.Rel(root, filepath.Clean(token)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return ""
}

// MarkChainBackfillPending records candidate paths the revision chain has
// never seen as pending backfill targets. chained is the chain's distinct
// path set the caller already fetched; a candidate it names is ignored, as
// is one already marked, and the map caps at chainBackfillMaxEntries.
// Best effort: a state failure drops the hint.
func (s *Store) MarkChainBackfillPending(candidates []string, chained map[string]bool) {
	if len(candidates) == 0 {
		return
	}
	_ = s.UpdateCaptureState(func(state *CaptureState) bool {
		changed := false
		if state.ChainBackfill == nil {
			state.ChainBackfill = map[string]string{}
		}
		for _, candidate := range candidates {
			if chained[candidate] {
				continue
			}
			if _, marked := state.ChainBackfill[candidate]; marked {
				continue
			}
			if len(state.ChainBackfill) >= chainBackfillMaxEntries {
				return changed
			}
			state.ChainBackfill[candidate] = ""
			changed = true
		}
		return changed
	})
}
