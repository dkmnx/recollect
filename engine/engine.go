// Package engine is the public facade over Recollect's transaction engine.
// It exposes only the types, errors, and constructor external adapters need.
// The implementation lives in recollect/internal/engine.
package engine

import internalengine "github.com/dkmnx/recollect/internal/engine"

// Types shared between an adapter and the engine.
type (
	AppendRequest  = internalengine.AppendRequest
	Config         = internalengine.Config
	CorrectRequest = internalengine.CorrectRequest
	DeleteRequest  = internalengine.DeleteRequest
	Engine         = internalengine.Engine
)

// Scope values selecting the shared store a request writes.
const ScopeProject = internalengine.ScopeProject

// Stable failures callers match with errors.Is.
var (
	ErrNotFound         = internalengine.ErrNotFound
	ErrRevisionConflict = internalengine.ErrRevisionConflict
	Open                = internalengine.Open
)
