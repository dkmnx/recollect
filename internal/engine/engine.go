// Package engine coordinates shared memory writes across processes. Facts stay
// authoritative Markdown files; private state lives in a reserved .engine
// directory beside each scope. Every mutation is one locked transaction.
package engine

import (
	"errors"
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"time"
)

// Stable engine failures. Callers match them with errors.Is.
var (
	ErrReadRequired     = errors.New("read_required")
	ErrRevisionConflict = errors.New("revision_conflict")
	ErrBusy             = errors.New("busy")
	ErrConflict         = errors.New("conflict")
	ErrNotFound         = errors.New("fact_not_found")
	ErrReasonRequired   = errors.New("reason_required")
	ErrStaleRequest     = errors.New("stale_request")
)

// Revision is an opaque token over a record's normalized path and bytes.
type Revision string

// Scope selects the shared store a request writes. The zero value means
// project scope.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

// Config binds an engine to one project store and one user store.
type Config struct {
	Store       *memory.Store
	ProjectDir  string
	UserDir     string
	LockTimeout time.Duration
}

// AppendRequest adds one fact. RequestID makes the append retry-safe.
type AppendRequest struct {
	RequestID   string
	Name        string
	Description string
	Body        string
	Reason      string
	Scope       Scope
}

// AppendResult is the durable outcome of an append.
type AppendResult struct {
	RequestID string   `json:"requestID"`
	Name      string   `json:"name"`
	Revision  Revision `json:"revision"`
}

// CorrectRequest replaces a fact body after a preceding read. For user scope,
// Body is the full desired user memory because user.md is one record.
type CorrectRequest struct {
	RequestID   string
	Name        string
	Expected    Revision
	Description string
	Body        string
	Scope       Scope
}

// FactResult is the durable outcome of a correction.
type FactResult struct {
	RequestID string   `json:"requestID"`
	Name      string   `json:"name"`
	Revision  Revision `json:"revision"`
}

// DeleteRequest removes a fact after a preceding read.
type DeleteRequest struct {
	RequestID string
	Name      string
	Expected  Revision
	Scope     Scope
}

// Cursor is one session's durable position. Harness and session identity keep
// concurrent sessions from sharing a marker.
type Cursor struct {
	HarnessID string `json:"harnessID"`
	SessionID string `json:"sessionID"`
	Position  int64  `json:"position"`
	Phase     string `json:"phase"`
}

// Engine owns transactions and scope locks over a memory store.
type Engine struct {
	store       *memory.Store
	projectDir  string
	userDir     string
	lockTimeout time.Duration
}

// Open binds an engine to the configured directories. Missing values fall
// back to the store's own paths and a five-second lock timeout.
func Open(cfg Config) *Engine {
	if cfg.ProjectDir == "" && cfg.Store != nil {
		cfg.ProjectDir = cfg.Store.ProjectDir()
	}
	if cfg.UserDir == "" && cfg.Store != nil {
		cfg.UserDir = filepath.Dir(cfg.Store.UserPath())
	}
	if cfg.Store == nil {
		cfg.Store = memory.NewStore(memory.Options{
			ProjectDir: cfg.ProjectDir,
			UserDir:    cfg.UserDir,
		})
	}
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = 5 * time.Second
	}
	return &Engine{store: cfg.Store, projectDir: cfg.ProjectDir, userDir: cfg.UserDir, lockTimeout: cfg.LockTimeout}
}

func scopeOrProject(scope Scope) Scope {
	if scope == ScopeUser {
		return ScopeUser
	}
	return ScopeProject
}

func (e *Engine) scopeDir(scope Scope) string {
	if scopeOrProject(scope) == ScopeUser {
		return e.userDir
	}
	return e.projectDir
}

func (e *Engine) lockPath(scope Scope) string {
	scope = scopeOrProject(scope)
	return filepath.Join(e.scopeDir(scope), ".engine", "locks", string(scope)+".lock")
}

// ReadRevision returns the revision of the named project fact.
func (e *Engine) ReadRevision(name string) (Revision, error) {
	return e.ReadRevisionForScope(ScopeProject, name)
}

// ReadRevisionForScope returns the revision of one record. User scope has one
// user.md record, so name is ignored. Every read recovers pending work first.
func (e *Engine) ReadRevisionForScope(scope Scope, name string) (Revision, error) {
	scope = scopeOrProject(scope)
	var result Revision
	err := e.withLock(scope, func() error {
		rel := "user.md"
		if scope == ScopeProject {
			fact, err := e.store.ReadFact(name)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(filepath.Join(fact.Path, fact.FileName()))
		}
		data, err := os.ReadFile(filepath.Join(e.scopeDir(scope), filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		result = hashRevision(rel, data)
		return nil
	})
	return result, err
}

// SaveCursor writes one session's cursor record. Session records are separate
// files, so they need no shared lock.
func (e *Engine) SaveCursor(cursor Cursor) error {
	name := memory.SlugKey(cursor.HarnessID, 48) + "--" + memory.SlugKey(cursor.SessionID, 48) + ".json"
	path := filepath.Join(e.projectDir, ".engine", "cursors", name)
	return writeAtomic(path, mustJSON(cursor))
}
