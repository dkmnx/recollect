// digest_store.go — digest persistence and pruning on the Store: one file
// per session id, overwritten in place, bounded by the newest-N count and
// the TTL, with aged-out files moved to archive/sessions/ so they stay
// readable but are never searched.

package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/fsatomic"
)

// The archive bounds. Archived digests are housekeeping copies, not
// searchable state, so they keep a generous count cap; daily index
// snapshots older than the retention window go. Non-date directories under
// archive/ (the migrator's legacy-* backups) are never touched.
const (
	archiveDigestKeep  = 200
	snapshotKeepDays   = 30
	legacyArchiveStemp = "legacy-"
)

// SessionsDir exposes the digest directory for the project.
func (s *Store) SessionsDir() string { return SessionsDir(s.projectDir) }

// WriteDigest stores one digest, overwriting the session's previous file,
// then prunes best-effort. The written path is returned.
func (s *Store) WriteDigest(digest Digest) (string, error) {
	if strings.TrimSpace(digest.SessionID) == "" {
		return "", &Error{Code: CodeMissingField, Message: "digest session id is required"}
	}
	if SlugKey(digest.SessionID, 32) == "" {
		return "", &Error{Code: CodeMissingField, Message: "digest session id has no usable characters"}
	}
	rendered := FormatDigest(digest)
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := containPath(SessionsDir(s.projectDir), s.digestFileName(digest))
	if err != nil {
		return "", err
	}
	if err := s.writeLocked(path, rendered); err != nil {
		return "", err
	}
	// Best effort: the write already landed, and a prune failure must not
	// fail it. The next write retries.
	_, _ = s.pruneDigestsLocked()
	return path, nil
}

// pruneArchiveLocked bounds the archive: archived session digests beyond
// archiveDigestKeep (newest first by name) and daily snapshot directories
// older than snapshotKeepDays are removed. Best-effort housekeeping.
func (s *Store) pruneArchiveLocked() {
	sessions := ArchiveSessionsDir(s.projectDir)
	if entries, err := os.ReadDir(sessions); err == nil {
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		if len(names) > archiveDigestKeep {
			for _, name := range names[archiveDigestKeep:] {
				_ = os.Remove(filepath.Join(sessions, name))
			}
		}
	}
	archive := ArchiveDir(s.projectDir)
	entries, err := os.ReadDir(archive)
	if err != nil {
		return
	}
	cutoff := s.now().AddDate(0, 0, -snapshotKeepDays)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == sessionsDirName || strings.HasPrefix(entry.Name(), legacyArchiveStemp) {
			continue
		}
		if stamp, err := time.Parse("2006-01-02", entry.Name()); err == nil && stamp.Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(archive, entry.Name()))
		}
	}
}

// ListDigests returns the searchable digests, newest capture first. Files
// without a Session line are ignored.
func (s *Store) ListDigests() ([]Digest, error) {
	entries, err := os.ReadDir(SessionsDir(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Errorf(CodeIO, "read digests: %v", err)
	}
	digests := make([]Digest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), digestFileExt) {
			continue
		}
		path := filepath.Join(SessionsDir(s.projectDir), entry.Name())
		content, err := readMemoryFile(path)
		if err != nil {
			return nil, err
		}
		digest, ok := ParseDigest(content, path)
		if !ok {
			continue
		}
		digests = append(digests, digest)
	}
	sortDigests(digests)
	return digests, nil
}

// ReadDigest returns the digest for one session id.
func (s *Store) ReadDigest(sessionID string) (Digest, error) {
	digests, err := s.ListDigests()
	if err != nil {
		return Digest{}, err
	}
	for _, digest := range digests {
		if digest.SessionID == sessionID {
			return digest, nil
		}
	}
	return Digest{}, Errorf(CodeDigestNotFound, "no digest for session %s", sessionID)
}

// PruneDigests applies the retention bounds and returns how many files
// moved to the archive. It is idempotent.
func (s *Store) PruneDigests() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneDigestsLocked()
}

func (s *Store) pruneDigestsLocked() (int, error) {
	s.pruneArchiveLocked()
	digests, err := s.ListDigests()
	if err != nil {
		return 0, err
	}
	_, archive := RetainDigests(digests, s.now(), DigestRetention{
		MaxCount: s.limits.MaxDigests,
		TTLDays:  s.limits.DigestTTLDays,
	})
	if len(archive) == 0 {
		return 0, nil
	}
	dir := ArchiveSessionsDir(s.projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, Errorf(CodeIO, "create digest archive: %v", err)
	}
	moved := 0
	for _, digest := range archive {
		dest := filepath.Join(dir, filepath.Base(digest.Path))
		if _, err := os.Lstat(dest); err == nil {
			// Already archived by an earlier prune: drop the duplicate.
			if err := os.Remove(digest.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return moved, Errorf(CodeIO, "remove pruned digest: %v", err)
			}
			moved++
			continue
		}
		if err := fsatomic.Replace(digest.Path, dest); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return moved, Errorf(CodeIO, "archive digest: %v", err)
		}
		moved++
	}
	return moved, nil
}

// digestFileName derives the stable per-session file name: the session start
// date plus the slugged session id, so a resumed session overwrites in place.
func (s *Store) digestFileName(digest Digest) string {
	date := digest.Date
	if date.IsZero() {
		date = digest.Updated
	}
	if date.IsZero() {
		date = s.now()
	}
	return date.Format("2006-01-02") + "-" + SlugKey(digest.SessionID, 32) + digestFileExt
}

// sortDigests orders newest capture first, session id as the stable tiebreak.
func sortDigests(digests []Digest) {
	sort.SliceStable(digests, func(i, j int) bool {
		left, right := digestStamp(digests[i]), digestStamp(digests[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return digests[i].SessionID < digests[j].SessionID
	})
}
