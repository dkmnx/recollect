package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"recollect/internal/memory"
	"strings"
	"time"
)

func (e *Engine) factRel(name string) (string, error) {
	fact, err := e.store.ReadFact(name)
	if err != nil {
		if memory.CodeOf(err) == memory.CodeFactNotFound {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return "", err
	}
	return filepath.ToSlash(filepath.Join(fact.Path, fact.FileName())), nil
}

func (e *Engine) commit(scope Scope, id, op, name, rel, before string, content []byte, result receiptRecord) error {
	after := ""
	if content != nil {
		after = string(hashRevision(rel, content))
	} else {
		content = []byte{}
	}
	intent := intentRecord{
		ID:      id,
		Scope:   string(scope),
		Op:      op,
		Name:    name,
		Path:    rel,
		Before:  before,
		After:   after,
		Content: string(content),
		Result:  result,
	}
	if err := writeAtomic(e.txPath(scope, id), mustJSON(intent)); err != nil {
		return err
	}
	if err := e.applyIntent(intent); err != nil {
		return err
	}
	if err := e.writeReceipt(scope, result); err != nil {
		return err
	}
	return os.Remove(e.txPath(scope, id))
}

func checkContent(description, body string) error {
	if (strings.TrimSpace(description) != "" && memory.IsFullyPrivate(description)) ||
		(strings.TrimSpace(body) != "" && memory.IsFullyPrivate(body)) {
		return fmt.Errorf("engine: memory content is fully private")
	}
	return nil
}

// AppendFact adds one fact. A retry with the same RequestID returns the
// recorded result without writing again.
func (e *Engine) AppendFact(req AppendRequest) (AppendResult, error) {
	var out AppendResult
	if strings.TrimSpace(req.RequestID) == "" {
		return out, fmt.Errorf("engine: append requires a request id")
	}
	scope := scopeOrProject(req.Scope)
	if scope == ScopeProject && strings.TrimSpace(req.Description) == "" {
		return out, fmt.Errorf("engine: fact description is required")
	}
	if scope == ScopeUser && strings.TrimSpace(req.Reason) == "" {
		return out, ErrReasonRequired
	}
	if err := checkContent(req.Description, req.Body); err != nil {
		return out, err
	}
	key := req.Name
	if strings.TrimSpace(key) == "" {
		key = req.Description
	}
	err := e.withLock(scope, func() error {
		if receipt, ok, err := e.checkedReceipt(scope, req.RequestID, "append", key); err != nil {
			return err
		} else if ok {
			out = AppendResult{RequestID: receipt.RequestID, Name: receipt.Name, Revision: Revision(receipt.Revision)}
			return nil
		}
		if scope == ScopeUser {
			currentData, exists, err := e.readTarget(ScopeUser, "user.md")
			if err != nil {
				return err
			}
			current := string(currentData)
			line := memory.StripPrivateContent(strings.TrimSpace(req.Body))
			if line == "" {
				line = memory.StripPrivateContent(strings.TrimSpace(req.Description))
			}
			reason := memory.StripPrivateContent(strings.TrimSpace(req.Reason))
			addition := "<!-- mem-append-reason: " + reason + " -->\n" + line
			content := addition
			if strings.TrimSpace(current) != "" {
				content = strings.TrimRight(current, " \t\r\n") + "\n" + addition
			}
			rel := "user.md"
			before := ""
			if exists {
				before = string(hashRevision(rel, currentData))
			}
			result := receiptRecord{
				RequestID: req.RequestID,
				Scope:     string(scope),
				Op:        "append",
				Key:       key,
				Name:      "user",
				Revision:  string(hashRevision(rel, []byte(content))),
			}
			if err := e.commit(scope, req.RequestID, "append", "user", rel, before, []byte(content), result); err != nil {
				return err
			}
			out = AppendResult{RequestID: result.RequestID, Name: result.Name, Revision: Revision(result.Revision)}
			return nil
		}

		facts, err := e.store.ListFacts()
		if err != nil {
			return err
		}
		names := make(map[string]bool, len(facts))
		for _, fact := range facts {
			names[fact.Name] = true
		}
		name := memory.FactSlug(req.Name)
		if name == "" {
			name = memory.FactSlug(req.Description)
		}
		if name == "" {
			name = memory.Fact{Description: req.Description}.FallbackName()
		}
		name = memory.NextKey(names, name)
		rel := name + ".md"
		prepared := memory.Fact{
			Name:        name,
			Description: memory.StripPrivateContent(memory.CollapseLine(req.Description)),
			Type:        memory.FactTypeProject,
			CapturedOn:  time.Now().Format(time.DateOnly),
			Body:        memory.StripPrivateContent(strings.Trim(req.Body, "\n")),
		}
		content := []byte(memory.FormatFact(prepared))
		result := receiptRecord{
			RequestID: req.RequestID,
			Scope:     string(scope),
			Op:        "append",
			Key:       key,
			Name:      name,
			Revision:  string(hashRevision(rel, content)),
		}
		if err := e.commit(scope, req.RequestID, "append", name, rel, "", content, result); err != nil {
			return err
		}
		out = AppendResult{RequestID: result.RequestID, Name: result.Name, Revision: Revision(result.Revision)}
		return nil
	})
	return out, err
}

// CorrectFact replaces a fact body. An absent revision returns read_required;
// a revision that is not current returns revision_conflict. Neither changes
// stored content.
func (e *Engine) CorrectFact(req CorrectRequest) (FactResult, error) {
	var out FactResult
	if strings.TrimSpace(req.RequestID) == "" {
		return out, fmt.Errorf("engine: correction requires a request id")
	}
	if req.Expected == "" {
		return out, ErrReadRequired
	}
	if err := checkContent(req.Description, req.Body); err != nil {
		return out, err
	}
	scope := scopeOrProject(req.Scope)
	err := e.withLock(scope, func() error {
		if receipt, ok, err := e.checkedReceipt(scope, req.RequestID, "correct", req.Name); err != nil {
			return err
		} else if ok {
			out = FactResult{RequestID: receipt.RequestID, Name: receipt.Name, Revision: Revision(receipt.Revision)}
			return nil
		}
		rel := "user.md"
		if scope == ScopeProject {
			var err error
			rel, err = e.factRel(req.Name)
			if err != nil {
				return err
			}
		}
		data, exists, err := e.readTarget(scope, rel)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, req.Name)
		}
		current := hashRevision(rel, data)
		if current != req.Expected {
			return ErrRevisionConflict
		}
		var content []byte
		if scope == ScopeUser {
			content = []byte(memory.StripPrivateContent(strings.TrimSpace(req.Body)))
		} else {
			fact := memory.ParseFact(string(data))
			if fact.Name == "" {
				fact.Name = memory.FactSlug(req.Name)
			}
			if collapsed := memory.CollapseLine(req.Description); collapsed != "" {
				fact.Description = memory.StripPrivateContent(collapsed)
			}
			if strings.TrimSpace(req.Body) != "" {
				fact.Body = memory.StripPrivateContent(strings.Trim(req.Body, "\n"))
			}
			content = []byte(memory.FormatFact(fact))
		}
		result := receiptRecord{
			RequestID: req.RequestID,
			Scope:     string(scope),
			Op:        "correct",
			Key:       req.Name,
			Name:      req.Name,
			Revision:  string(hashRevision(rel, content)),
		}
		if err := e.commit(scope, req.RequestID, "correct", req.Name, rel, string(current), content, result); err != nil {
			return err
		}
		out = FactResult{RequestID: result.RequestID, Name: result.Name, Revision: Revision(result.Revision)}
		return nil
	})
	return out, err
}

// DeleteFact removes a fact after a preceding read. It shares the revision
// preconditions of a correction.
func (e *Engine) DeleteFact(req DeleteRequest) error {
	if strings.TrimSpace(req.RequestID) == "" {
		return fmt.Errorf("engine: deletion requires a request id")
	}
	if req.Expected == "" {
		return ErrReadRequired
	}
	scope := scopeOrProject(req.Scope)
	return e.withLock(scope, func() error {
		if _, ok, err := e.checkedReceipt(scope, req.RequestID, "delete", req.Name); err != nil {
			return err
		} else if ok {
			return nil
		}
		rel := "user.md"
		if scope == ScopeProject {
			var err error
			rel, err = e.factRel(req.Name)
			if err != nil {
				return err
			}
		}
		data, exists, err := e.readTarget(scope, rel)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, req.Name)
		}
		current := hashRevision(rel, data)
		if current != req.Expected {
			return ErrRevisionConflict
		}
		result := receiptRecord{
			RequestID: req.RequestID,
			Scope:     string(scope),
			Op:        "delete",
			Key:       req.Name,
			Name:      req.Name,
		}
		return e.commit(scope, req.RequestID, "delete", req.Name, rel, string(current), nil, result)
	})
}
