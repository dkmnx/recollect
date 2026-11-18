package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"recollect/internal/fsatomic"
	"recollect/internal/oslock"
	"time"
)

// Private state layout and versions. External readers must ignore .engine.
const (
	engineDirName  = ".engine"
	schemaVersion  = "1"
	txDirName      = "tx"
	receiptDirName = "receipts"
	staleDirName   = "stale"

	// A receipt is replayable for 30 days, matching digest retention. A
	// tombstone then blocks stale retries for another 30 days before removal.
	receiptRetention = 30 * 24 * time.Hour
	staleRetention   = 30 * 24 * time.Hour
)

// Receipts carry the committed result and the time it was written.
type receiptRecord struct {
	RequestID string `json:"requestID"`
	Scope     string `json:"scope"`
	Op        string `json:"op,omitempty"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Revision  string `json:"revision"`
	Timestamp string `json:"timestamp,omitempty"`
}

// A staleRecord blocks a late retry after its receipt has been pruned.
type staleRecord struct {
	RequestID string `json:"requestID"`
	PrunedAt  string `json:"prunedAt"`
}

// An intent holds the sanitized bytes of one pending write plus the before and
// after hashes recovery needs to tell an unapplied write from an external edit.
type intentRecord struct {
	ID      string        `json:"id"`
	Scope   string        `json:"scope"`
	Op      string        `json:"op"`
	Name    string        `json:"name"`
	Path    string        `json:"path"`
	Before  string        `json:"before"`
	After   string        `json:"after"`
	Content string        `json:"content"`
	Result  receiptRecord `json:"result"`
}

func hashRevision(rel string, data []byte) Revision {
	sum := sha256.Sum256([]byte(rel + "\x00" + string(data)))
	return Revision(hex.EncodeToString(sum[:]))
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic("engine: encode state: " + err.Error())
	}
	return data
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("engine: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("engine: temp in %s: %w", filepath.Dir(path), err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("engine: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("engine: close temp: %w", err)
	}
	if err := fsatomic.Replace(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("engine: replace %s: %w", path, err)
	}
	return nil
}

func (e *Engine) statePath(scope Scope, parts ...string) string {
	return filepath.Join(append([]string{e.scopeDir(scope), engineDirName}, parts...)...)
}

// stateID maps an opaque request ID to a safe file name. Request IDs reach the
// engine from adapters, so they never become path components directly.
func stateID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func (e *Engine) txPath(scope Scope, id string) string {
	return e.statePath(scope, txDirName, stateID(id)+".intent.json")
}

func (e *Engine) receiptPath(scope Scope, id string) string {
	return e.statePath(scope, receiptDirName, stateID(id)+".json")
}

func (e *Engine) stalePath(scope Scope, id string) string {
	return e.statePath(scope, staleDirName, stateID(id)+".json")
}

func (e *Engine) ensureSchema(scope Scope) error {
	path := e.statePath(scope, "schema")
	data, err := os.ReadFile(path)
	if err == nil {
		if string(data) != schemaVersion+"\n" && string(data) != schemaVersion {
			return fmt.Errorf("engine: unknown schema in %s", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("engine: read schema: %w", err)
	}
	return writeAtomic(path, []byte(schemaVersion+"\n"))
}

func (e *Engine) readReceipt(scope Scope, id string) (receiptRecord, bool, error) {
	data, err := os.ReadFile(e.receiptPath(scope, id))
	if errors.Is(err, os.ErrNotExist) {
		tombstone, tombErr := os.ReadFile(e.stalePath(scope, id))
		if tombErr == nil {
			var stale staleRecord
			if json.Unmarshal(tombstone, &stale) == nil && stale.RequestID != "" {
				return receiptRecord{}, false, fmt.Errorf("%w: %s", ErrStaleRequest, id)
			}
		}
		return receiptRecord{}, false, nil
	}
	if err != nil {
		return receiptRecord{}, false, fmt.Errorf("engine: read receipt: %w", err)
	}
	var receipt receiptRecord
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receiptRecord{}, false, fmt.Errorf("engine: parse receipt %s: %w", id, err)
	}
	return receipt, true, nil
}

func (e *Engine) checkedReceipt(scope Scope, id, op, name string) (receiptRecord, bool, error) {
	receipt, ok, err := e.readReceipt(scope, id)
	if err != nil || !ok {
		return receipt, ok, err
	}
	if receipt.Scope != string(scopeOrProject(scope)) ||
		receipt.Op != op || receipt.Key != name {
		return receiptRecord{}, false, fmt.Errorf("%w: request id %q reused for %s/%s", ErrConflict, id, op, name)
	}
	return receipt, true, nil
}

func (e *Engine) writeReceipt(scope Scope, receipt receiptRecord) error {
	if receipt.Timestamp == "" {
		receipt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return writeAtomic(e.receiptPath(scope, receipt.RequestID), mustJSON(receipt))
}

func (e *Engine) readTarget(scope Scope, rel string) ([]byte, bool, error) {
	path := filepath.Join(e.scopeDir(scope), filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("engine: read %s: %w", path, err)
	}
	return data, true, nil
}

func (e *Engine) applyIntent(intent intentRecord) error {
	data, exists, err := e.readTarget(Scope(intent.Scope), intent.Path)
	if err != nil {
		return err
	}
	if intent.After == "" {
		if exists {
			path := filepath.Join(e.scopeDir(Scope(intent.Scope)), filepath.FromSlash(intent.Path))
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("engine: remove %s: %w", path, err)
			}
		}
	} else if !exists || string(hashRevision(intent.Path, data)) != intent.After {
		path := filepath.Join(e.scopeDir(Scope(intent.Scope)), filepath.FromSlash(intent.Path))
		if err := writeAtomic(path, []byte(intent.Content)); err != nil {
			return err
		}
	}
	if intent.Scope == string(ScopeProject) {
		if err := e.store.RegenerateIndex(); err != nil {
			return fmt.Errorf("engine: regenerate index: %w", err)
		}
	}
	return nil
}

func (e *Engine) recoverIntent(intent intentRecord, path string) error {
	if _, ok, err := e.readReceipt(Scope(intent.Scope), intent.ID); err != nil || ok {
		if err != nil {
			return err
		}
		return os.Remove(path)
	}
	data, exists, err := e.readTarget(Scope(intent.Scope), intent.Path)
	if err != nil {
		return err
	}
	current := ""
	if exists {
		current = string(hashRevision(intent.Path, data))
	}
	switch {
	case intent.After == "" && !exists, intent.After != "" && current == intent.After:
		// The write landed; only the receipt was lost. Reapply to rebuild
		// derived state such as the project index before acknowledging.
		if err := e.applyIntent(intent); err != nil {
			return err
		}
	case current == intent.Before:
		if err := e.applyIntent(intent); err != nil {
			return err
		}
	default:
		// The file matches neither state: an external editor owns it now.
		return fmt.Errorf("%w: %s", ErrConflict, intent.ID)
	}
	intent.Result.Scope = intent.Scope
	if intent.Result.Key == "" {
		intent.Result.Key = intent.Name
	}
	if err := e.writeReceipt(Scope(intent.Scope), intent.Result); err != nil {
		return err
	}
	return os.Remove(path)
}

func (e *Engine) pruneReceipts(scope Scope) error {
	dir := e.statePath(scope, receiptDirName)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("engine: read receipts: %w", err)
	}
	cutoff := time.Now().Add(-receiptRetention)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("engine: read receipt %s: %w", entry.Name(), err)
		}
		var receipt receiptRecord
		if json.Unmarshal(data, &receipt) != nil || receipt.Timestamp == "" || receipt.RequestID == "" {
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, receipt.Timestamp)
		if err != nil || !stamp.Before(cutoff) {
			continue
		}
		tombPath := e.stalePath(scope, receipt.RequestID)
		validTombstone := false
		tombData, tombErr := os.ReadFile(tombPath)
		if tombErr == nil {
			var tomb staleRecord
			validTombstone = json.Unmarshal(tombData, &tomb) == nil &&
				tomb.RequestID == receipt.RequestID
		} else if !errors.Is(tombErr, os.ErrNotExist) {
			return fmt.Errorf("engine: read tombstone %s: %w", tombPath, tombErr)
		}
		if !validTombstone {
			tomb := staleRecord{
				RequestID: receipt.RequestID,
				PrunedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := writeAtomic(tombPath, mustJSON(tomb)); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("engine: prune receipt %s: %w", entry.Name(), err)
		}
	}

	staleDir := e.statePath(scope, staleDirName)
	entries, err = os.ReadDir(staleDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("engine: read stale records: %w", err)
	}
	staleCutoff := time.Now().Add(-staleRetention)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(staleDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("engine: read stale record %s: %w", entry.Name(), err)
		}
		var stale staleRecord
		if json.Unmarshal(data, &stale) != nil || stale.PrunedAt == "" {
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, stale.PrunedAt)
		if err == nil && stamp.Before(staleCutoff) {
			if err := os.Remove(filepath.Join(staleDir, entry.Name())); err != nil {
				return fmt.Errorf("engine: prune stale record %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func (e *Engine) recoverLocked(scopes ...Scope) error {
	for _, scope := range scopes {
		if err := e.ensureSchema(scope); err != nil {
			return err
		}
		dir := e.statePath(scope, txDirName)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			entries = nil
		} else if err != nil {
			return fmt.Errorf("engine: read transactions: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("engine: read intent %s: %w", entry.Name(), err)
			}
			var intent intentRecord
			if err := json.Unmarshal(data, &intent); err != nil {
				return fmt.Errorf("engine: parse intent %s: %w", entry.Name(), err)
			}
			if err := e.recoverIntent(intent, path); err != nil {
				return err
			}
		}
		if err := e.pruneReceipts(scope); err != nil {
			return err
		}
	}
	return nil
}

// Recover settles unfinished transactions in both scopes. User scope is
// locked first because operations that need both scopes keep that order.
func (e *Engine) Recover() error {
	userRelease, err := oslock.Lock(e.lockPath(ScopeUser), e.lockTimeout)
	if err != nil {
		return fmt.Errorf("%w: user scope: %v", ErrBusy, err)
	}
	defer userRelease()
	projectRelease, err := oslock.Lock(e.lockPath(ScopeProject), e.lockTimeout)
	if err != nil {
		return fmt.Errorf("%w: project scope: %v", ErrBusy, err)
	}
	defer projectRelease()
	return e.recoverLocked(ScopeUser, ScopeProject)
}

func (e *Engine) withLock(scope Scope, work func() error) error {
	release, err := oslock.Lock(e.lockPath(scope), e.lockTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	defer release()
	if err := e.recoverLocked(scope); err != nil {
		return err
	}
	return work()
}
