package engine_test

// audit: behavior - Recovery settles an intent at each durable step and preserves external edits.
// audit: regression - an acknowledged write survives a restart without a second mutation.
// audit: coverage gap - crash boundaries between intent, apply, result, and acknowledgement.
// audit: seam - states are built by writing the documented .engine files, with no test-only export.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"recollect/internal/engine"
	"recollect/internal/memory"
	"strings"
	"testing"
	"time"
)

type txResult struct {
	RequestID string `json:"requestID"`
	Name      string `json:"name"`
	Revision  string `json:"revision"`
}

type txIntent struct {
	ID      string   `json:"id"`
	Scope   string   `json:"scope"`
	Op      string   `json:"op"`
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Content string   `json:"content"`
	Result  txResult `json:"result"`
}

func revisionOf(rel string, data []byte) string {
	sum := sha256.Sum256([]byte(rel + "\x00" + string(data)))
	return hex.EncodeToString(sum[:])
}

func stateIDFor(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRecoverAtEachDurableStep(t *testing.T) {
	steps := []struct {
		name        string
		writeResult bool
		applyWrite  bool
		external    bool
		wantBody    string
		wantErr     error
	}{
		{name: "intent only", wantBody: "anchor changed body"},
		{name: "applied before result", applyWrite: true, wantBody: "anchor changed body"},
		{name: "result before acknowledge", applyWrite: true, writeResult: true, wantBody: "anchor changed body"},
		{name: "external edit", external: true, wantBody: "external edit body", wantErr: engine.ErrConflict},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			eng, store, projectDir, _ := newTestEngine(t)
			added, err := eng.AppendFact(engine.AppendRequest{
				RequestID:   "anchor-add",
				Description: "anchor",
				Body:        "anchor original body",
			})
			if err != nil {
				t.Fatalf("append anchor: %v", err)
			}
			fact, err := store.ReadFact(added.Name)
			if err != nil {
				t.Fatalf("read anchor: %v", err)
			}
			rel := filepath.ToSlash(filepath.Join(fact.Path, fact.FileName()))
			target := filepath.Join(projectDir, filepath.FromSlash(rel))
			original, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read anchor file: %v", err)
			}

			changed := memory.FormatFact(memory.Fact{
				Name:        fact.Name,
				Description: fact.Description,
				Type:        fact.Type,
				Scene:       fact.Scene,
				CapturedOn:  fact.CapturedOn,
				Body:        "anchor changed body",
			})
			intent := txIntent{
				ID:      "tx-step",
				Scope:   "project",
				Op:      "correct",
				Name:    added.Name,
				Path:    rel,
				Before:  revisionOf(rel, original),
				After:   revisionOf(rel, []byte(changed)),
				Content: changed,
				Result: txResult{
					RequestID: "tx-step",
					Name:      added.Name,
					Revision:  revisionOf(rel, []byte(changed)),
				},
			}
			intentPath := filepath.Join(projectDir, ".engine", "tx", stateIDFor(intent.ID)+".intent.json")
			receiptPath := filepath.Join(projectDir, ".engine", "receipts", stateIDFor(intent.ID)+".json")
			writeJSON(t, intentPath, intent)
			if step.applyWrite || step.writeResult {
				if err := os.WriteFile(target, []byte(changed), 0o600); err != nil {
					t.Fatalf("apply intent: %v", err)
				}
			}
			if step.writeResult {
				writeJSON(t, receiptPath, intent.Result)
			}
			if step.external {
				external := memory.FormatFact(memory.Fact{
					Name:        fact.Name,
					Description: fact.Description,
					Type:        fact.Type,
					Scene:       fact.Scene,
					CapturedOn:  fact.CapturedOn,
					Body:        "external edit body",
				})
				if err := os.WriteFile(target, []byte(external), 0o600); err != nil {
					t.Fatalf("external edit: %v", err)
				}
			}

			err = eng.Recover()
			if step.wantErr != nil {
				if !errors.Is(err, step.wantErr) {
					t.Fatalf("recover: got %v, want %v", err, step.wantErr)
				}
			} else if err != nil {
				t.Fatalf("recover: %v", err)
			}

			current, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read target after recovery: %v", err)
			}
			currentFact := memory.ParseFact(string(current))
			if currentFact.Body != step.wantBody {
				t.Fatalf("body after recovery = %q, want %q", currentFact.Body, step.wantBody)
			}
			if step.wantErr == nil {
				if _, err := os.Stat(receiptPath); err != nil {
					t.Fatalf("receipt missing after recovery: %v", err)
				}
				if _, err := os.Stat(intentPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("settled intent should be removed: %v", err)
				}
			} else if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("conflicting transaction must not get a receipt: %v", err)
			}
		})
	}
}

func TestRecoveryRebuildsIndexAfterWriteLanded(t *testing.T) {
	eng, store, projectDir, _ := newTestEngine(t)
	rel := "recovered.md"
	content := memory.FormatFact(memory.Fact{
		Name:        "recovered",
		Description: "recovered fact",
		Type:        memory.FactTypeProject,
		CapturedOn:  "2026-11-18",
		Body:        "recovered body",
	})
	target := filepath.Join(projectDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	intent := txIntent{
		ID:      "recover-index",
		Scope:   "project",
		Op:      "append",
		Name:    "recovered",
		Path:    rel,
		Before:  "",
		After:   revisionOf(rel, []byte(content)),
		Content: content,
		Result: txResult{
			RequestID: "recover-index",
			Name:      "recovered",
			Revision:  revisionOf(rel, []byte(content)),
		},
	}
	writeJSON(t, filepath.Join(projectDir, ".engine", "tx", stateIDFor(intent.ID)+".intent.json"), intent)
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	index, err := store.ReadIndex()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(index, "recovered fact") {
		t.Fatalf("index after recovery = %q, want the recovered fact", index)
	}
}

func TestUserAppendIntentionRecovers(t *testing.T) {
	eng, store, _, userDir := newTestEngine(t)
	current := "existing note\n"
	newContent := "existing note\n<!-- mem-append-reason: style preference -->\nprefer tabs"
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "user.md"), []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}
	intent := txIntent{
		ID:      "user-recover",
		Scope:   "user",
		Op:      "append",
		Name:    "user",
		Path:    "user.md",
		Before:  revisionOf("user.md", []byte(current)),
		After:   revisionOf("user.md", []byte(newContent)),
		Content: newContent,
		Result: txResult{
			RequestID: "user-recover",
			Name:      "user",
			Revision:  revisionOf("user.md", []byte(newContent)),
		},
	}
	writeJSON(t, filepath.Join(userDir, ".engine", "tx", stateIDFor(intent.ID)+".intent.json"), intent)
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover user append: %v", err)
	}
	content, err := store.ReadUser()
	if err != nil {
		t.Fatalf("read user memory: %v", err)
	}
	if !strings.Contains(content, "prefer tabs") {
		t.Fatalf("user memory after recovery = %q, want the append", content)
	}
}

func TestRecoverFromFreshEngineKeepsAcknowledgedAppend(t *testing.T) {
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	userDir := filepath.Join(base, "user")
	makeEngine := func() *engine.Engine {
		store := memory.NewStore(memory.Options{
			ProjectDir: projectDir,
			UserDir:    userDir,
			Meta:       memory.ProjectMetaFor(projectDir),
		})
		return engine.Open(engine.Config{
			Store:       store,
			ProjectDir:  projectDir,
			UserDir:     userDir,
			LockTimeout: time.Second,
		})
	}
	added, err := makeEngine().AppendFact(engine.AppendRequest{
		RequestID:   "persisted-1",
		Description: "persisted fact",
		Body:        "persisted body",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	restarted := makeEngine()
	if err := restarted.Recover(); err != nil {
		t.Fatalf("recover after restart: %v", err)
	}
	again, err := restarted.AppendFact(engine.AppendRequest{
		RequestID:   "persisted-1",
		Description: "persisted fact",
		Body:        "persisted body",
	})
	if err != nil {
		t.Fatalf("retry after restart: %v", err)
	}
	if again != added {
		t.Fatalf("retry after restart = %+v, want %+v", again, added)
	}
}
