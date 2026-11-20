package memory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dkmnx/recollect/internal/fsatomic"
)

// Options configures a Store. UserDir holds the global user.md and
// ProjectDir holds this project's files; Meta is the project identity
// recorded once in project.json. Now is the injected clock (nil means
// time.Now) and Limits zero fields fall back to DefaultLimits. ReadOnly
// skips construction-time legacy migration, so a read-only caller never
// copies, removes, or rebuilds files under the memory tree.
type Options struct {
	UserDir       string
	ProjectDir    string
	Meta          ProjectMeta
	Now           func() time.Time
	Limits        Limits
	SearchBackend string
	Embedder      EmbeddingProvider
	ReadOnly      bool
}

// Store owns every read and write of the memory files. Writers serialize on
// one mutex and land through a temp file plus rename, so a reader sees the
// old or the new file, never a partial one. No write is ever refused for
// size or count (ADR-005): the fact files, the index, user.md, and the
// digests all grow without caps.
type Store struct {
	userDir       string
	projectDir    string
	meta          ProjectMeta
	now           func() time.Time
	limits        Limits
	searchBackend string
	embedder      EmbeddingProvider
	indexMu       sync.Mutex
	searchIndex   *searchIndex
	searchCache   map[string]searchFact // Guarded by mu.
	mu            sync.Mutex
	// migrateErr records a failed legacy migration; the legacy files stay
	// intact (copy-first), and /memory status surfaces the failure.
	migrateErr error
}

// MigrateError returns the error from the automatic legacy-format
// migration, if one ran and failed.
func (s *Store) MigrateError() error { return s.migrateErr }

// NewStore builds a store over the given directories. A legacy-format
// project directory is migrated in place on construction (copy-first); a
// failed migration is recorded and surfaced by MigrateError.
func NewStore(opts Options) *Store {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	store := &Store{
		userDir:       opts.UserDir,
		projectDir:    opts.ProjectDir,
		meta:          opts.Meta,
		now:           now,
		limits:        opts.Limits.withDefaults(),
		searchBackend: opts.SearchBackend,
		embedder:      opts.Embedder,
	}
	if !opts.ReadOnly {
		if _, err := store.MigrateLegacyIfPresent(); err != nil {
			store.migrateErr = err
		}
	}
	return store
}

// NewReadOnlyStore builds a store that reads memory without migrating a
// legacy tree. Read-only callers, such as the prompt dump, use it so a seeded
// legacy store stays byte-for-byte intact.
func NewReadOnlyStore(opts Options) *Store {
	opts.ReadOnly = true
	return NewStore(opts)
}

// ProjectDir is the store's project memory directory.
func (s *Store) ProjectDir() string { return s.projectDir }

// UserPath is the global user memory file path.
func (s *Store) UserPath() string { return UserFile(s.userDir) }

// Limits returns the resolved bounds (search caps, injection caps, digest
// retention, manifest budgets).
func (s *Store) Limits() Limits {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits
}

// SetDigestLimits updates the digest bounds in place. One store is shared by
// the memory tool, capture, and /memory, and the bounds are settings-driven,
// so they are refreshed at write time rather than fixed at construction.
// Zero or negative values keep every digest / disable the age bound.
func (s *Store) SetDigestLimits(maxDigests, ttlDays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits.MaxDigests = maxDigests
	s.limits.DigestTTLDays = ttlDays
}

// Meta returns the project identity (the zero value before the first
// EnsureProject).
func (s *Store) Meta() ProjectMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

// ReadUser returns the global user memory, or "" when absent.
func (s *Store) ReadUser() (string, error) { return readMemoryFile(UserFile(s.userDir)) }

// ReadUserBytes reads user.md through the same file checks as ReadUser and
// returns the bytes a revision can be computed from. A missing file returns
// os.ErrNotExist.
func (s *Store) ReadUserBytes() ([]byte, error) {
	path := UserFile(s.userDir)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, &Error{Code: CodeFileInvalid, Message: "memory file is not a regular file: " + path}
	}
	return os.ReadFile(path)
}

// SearchUser finds matches in the user memory, capped by MaxSearchResults.
func (s *Store) SearchUser(query string) ([]Match, error) {
	content, err := s.ReadUser()
	if err != nil {
		return nil, err
	}
	return SearchLines(filterSearchText(content), query, s.limits.MaxSearchResults), nil
}

// userAppendLocks serializes user.md read-modify-write cycles across every
// Store sharing the same user file. The app's project store and the
// per-agent stores hold separate instance mutexes, so only a per-path lock
// can stop their AppendUser cycles from interleaving (two stores over one
// user.md silently lose appends).
var userAppendLocks sync.Map

func userAppendLock(userDir string) *sync.Mutex {
	path := UserFile(userDir)
	lock, _ := userAppendLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// AppendUser adds a reasoned entry to the global user memory. The reason is
// recorded beside the content so a reader can tell durable additions from
// noise; size is never refused. The per-path lock brackets the
// read-modify-write so stores sharing this user file serialize on it before
// their instance mutexes.
func (s *Store) AppendUser(reason, content string) (int, error) {
	reason = StripPrivateContent(strings.TrimSpace(reason))
	if reason == "" {
		return 0, &Error{Code: CodeReasonRequired, Message: "user memory append requires a reason"}
	}
	if IsFullyPrivate(reason) {
		return 0, Errorf(CodePrivateRejected, "user memory content is fully private")
	}
	lock := userAppendLock(s.userDir)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.ReadUser()
	if err != nil {
		return 0, err
	}
	content = StripPrivateContent(content)
	if strings.TrimSpace(content) != "" && IsFullyPrivate(content) {
		return 0, Errorf(CodePrivateRejected, "user memory content is fully private")
	}
	next := appendWithSeparator(current, "<!-- mem-append-reason: "+reason+" -->\n"+content)
	if err := s.writeLocked(UserFile(s.userDir), next); err != nil {
		return 0, err
	}
	return len(next), nil
}

// EnsureProject writes project.json on first use and reports whether it
// created the file. Later calls keep the original bytes untouched.
func (s *Store) EnsureProject(meta ProjectMeta) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = meta
	return s.ensureProjectLocked()
}

// ReadProjectMeta returns the recorded project identity; exists is false
// when the project has never been written to.
func (s *Store) ReadProjectMeta() (ProjectMeta, bool, error) {
	data, err := os.ReadFile(ProjectMetaFile(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return ProjectMeta{}, false, nil
	}
	if err != nil {
		return ProjectMeta{}, false, Errorf(CodeIO, "read project.json: %v", err)
	}
	var meta ProjectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ProjectMeta{}, false, Errorf(CodeIO, "parse project.json: %v", err)
	}
	return meta, true, nil
}

// snapshotMainLocked copies the current index file; no-op when today's
// snapshot exists.
func (s *Store) snapshotMainLocked() (string, bool, error) {
	content, err := readMemoryFile(IndexFile(s.projectDir))
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(ArchiveDir(s.projectDir), s.now().Format("2006-01-02"), indexFileName)
	if _, err := os.Lstat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, Errorf(CodeIO, "stat snapshot: %v", err)
	}
	if err := s.writeLocked(path, content); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// ensureProjectLocked writes project.json once.
func (s *Store) ensureProjectLocked() (bool, error) {
	path := ProjectMetaFile(s.projectDir)
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, Errorf(CodeIO, "stat project.json: %v", err)
	}
	if s.meta.Root == "" {
		return false, Errorf(CodeIO, "project root is unknown for %s", s.projectDir)
	}
	meta := s.meta
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = s.now()
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return false, Errorf(CodeIO, "encode project.json: %v", err)
	}
	if err := s.writeLocked(path, string(data)+"\n"); err != nil {
		return false, err
	}
	return true, nil
}

// writeLocked writes through a same-directory temp file and rename.
func (s *Store) writeLocked(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Errorf(CodeIO, "create %s: %v", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return Errorf(CodeIO, "create temp in %s: %v", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return Errorf(CodeIO, "write temp: %v", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return Errorf(CodeIO, "close temp: %v", err)
	}
	// Windows cannot rename onto a file another process holds open. A
	// reader, a file watcher, or a virus scanner that touches the target
	// would lose the save, so the rename retries that hold.
	if err := fsatomic.Replace(tmpName, path); err != nil {
		os.Remove(tmpName)
		return Errorf(CodeIO, "rename: %v", err)
	}
	return nil
}

// readMemoryFile returns "" for a missing file and refuses symlinks and
// non-regular files, so memory reads can never follow a planted link.
func readMemoryFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", Errorf(CodeIO, "stat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", &Error{Code: CodeFileInvalid, Message: "memory file is not a regular file: " + path}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", Errorf(CodeIO, "read %s: %v", path, err)
	}
	return string(data), nil
}

// appendWithSeparator adds a newline only when the existing text has content.
func appendWithSeparator(current, addition string) string {
	if strings.TrimSpace(current) == "" {
		return addition
	}
	return strings.TrimRight(current, " \t\r\n") + "\n" + addition
}
