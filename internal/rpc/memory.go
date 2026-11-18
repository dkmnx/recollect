package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/dkmnx/recollect/internal/engine"
	"github.com/dkmnx/recollect/internal/memory"
)

type executeParams struct {
	Target      string `json:"target"`
	Operation   string `json:"operation"`
	RequestID   string `json:"requestID"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Reason      string `json:"reason"`
	Revision    string `json:"revision"`
	Query       string `json:"query"`
	Scene       string `json:"scene"`
	Limit       int    `json:"limit"`
}

type factView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type executeResult struct {
	Target      string         `json:"target"`
	Operation   string         `json:"operation"`
	Name        string         `json:"name,omitempty"`
	RequestID   string         `json:"requestID,omitempty"`
	Revision    string         `json:"revision,omitempty"`
	Content     string         `json:"content,omitempty"`
	Fact        *factView      `json:"fact,omitempty"`
	Matches     []factView     `json:"matches,omitempty"`
	LineMatches []memory.Match `json:"lineMatches,omitempty"`
}

func (s *server) execute(req Request) error {
	var p executeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid memory.execute params")
	}
	if p.Target != "project" && p.Target != "user" {
		return s.invalid(req.ID, "target must be project or user")
	}
	switch p.Operation {
	case "read", "search", "append", "correct", "delete":
	default:
		return s.invalid(req.ID, "unsupported operation")
	}

	scope := engine.ScopeProject
	if p.Target == "user" {
		scope = engine.ScopeUser
	}
	switch p.Operation {
	case "read":
		return s.executeRead(req, p, scope)
	case "search":
		return s.executeSearch(req, p, scope)
	case "append":
		return s.executeAppend(req, p, scope)
	case "correct":
		return s.executeCorrect(req, p, scope)
	default:
		return s.executeDelete(req, p, scope)
	}
}

func (s *server) executeRead(req Request, p executeParams, scope engine.Scope) error {
	out := executeResult{Target: p.Target, Operation: "read"}
	if scope == engine.ScopeUser {
		content, err := s.deps.Store.ReadUser()
		if err != nil {
			return s.wrap(req.ID, err, storageFailure)
		}
		rev, err := s.deps.Engine.ReadRevisionForScope(scope, "")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.wrap(req.ID, err, engineFailure)
		}
		out.Name = "user"
		out.Content = content
		out.Revision = string(rev)
		return s.reply(req.ID, out, nil)
	}
	if strings.TrimSpace(p.Name) == "" {
		return s.invalid(req.ID, "name is required for read")
	}
	fact, err := s.deps.Store.ReadFact(p.Name)
	if err != nil {
		return s.wrap(req.ID, err, engineFailure)
	}
	rev, err := s.deps.Engine.ReadRevisionForScope(scope, p.Name)
	if err != nil {
		return s.wrap(req.ID, err, engineFailure)
	}
	out.Name = fact.Name
	out.Revision = string(rev)
	out.Content = memory.FormatFact(fact)
	out.Fact = &factView{Name: fact.Name, Description: fact.Description, Body: fact.Body}
	return s.reply(req.ID, out, nil)
}

func (s *server) executeSearch(req Request, p executeParams, scope engine.Scope) error {
	if strings.TrimSpace(p.Query) == "" {
		return s.invalid(req.ID, "query is required for search")
	}
	out := executeResult{Target: p.Target, Operation: "search"}
	if scope == engine.ScopeUser {
		matches, err := s.deps.Store.SearchUser(p.Query)
		if err != nil {
			return s.wrap(req.ID, err, storageFailure)
		}
		out.LineMatches = matches
		return s.reply(req.ID, out, nil)
	}
	matches, err := s.deps.Store.SearchFacts(p.Query, p.Scene, p.Limit)
	if err != nil {
		return s.wrap(req.ID, err, storageFailure)
	}
	for _, match := range matches {
		out.Matches = append(out.Matches, factView{
			Name: match.Name, Description: match.Description, Body: match.Field,
		})
	}
	return s.reply(req.ID, out, nil)
}

func (s *server) executeAppend(req Request, p executeParams, scope engine.Scope) error {
	if strings.TrimSpace(p.RequestID) == "" {
		return s.invalid(req.ID, "requestID is required for append")
	}
	res, err := s.deps.Engine.AppendFact(engine.AppendRequest{
		RequestID: p.RequestID, Name: p.Name, Description: p.Description,
		Body: p.Body, Reason: p.Reason, Scope: scope,
	})
	if err != nil {
		return s.wrap(req.ID, err, engineFailure)
	}
	return s.reply(req.ID, mutationResult(p, res.Name, res.RequestID, string(res.Revision)), nil)
}

func (s *server) executeCorrect(req Request, p executeParams, scope engine.Scope) error {
	if strings.TrimSpace(p.RequestID) == "" {
		return s.invalid(req.ID, "requestID is required for correction")
	}
	res, err := s.deps.Engine.CorrectFact(engine.CorrectRequest{
		RequestID: p.RequestID, Name: p.Name, Expected: engine.Revision(p.Revision),
		Description: p.Description, Body: p.Body, Scope: scope,
	})
	if err != nil {
		return s.wrap(req.ID, err, engineFailure)
	}
	return s.reply(req.ID, mutationResult(p, res.Name, res.RequestID, string(res.Revision)), nil)
}

func (s *server) executeDelete(req Request, p executeParams, scope engine.Scope) error {
	if strings.TrimSpace(p.RequestID) == "" {
		return s.invalid(req.ID, "requestID is required for deletion")
	}
	err := s.deps.Engine.DeleteFact(engine.DeleteRequest{
		RequestID: p.RequestID, Name: p.Name,
		Expected: engine.Revision(p.Revision), Scope: scope,
	})
	if err != nil {
		return s.wrap(req.ID, err, engineFailure)
	}
	return s.reply(req.ID, mutationResult(p, p.Name, p.RequestID, ""), nil)
}

func mutationResult(p executeParams, name, requestID, revision string) executeResult {
	return executeResult{
		Target: p.Target, Operation: p.Operation,
		Name: name, RequestID: requestID, Revision: revision,
	}
}

// snapshotHash derives an opaque snapshot ID from rendered content.
func snapshotHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}
