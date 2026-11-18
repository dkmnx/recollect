// Package jobs turns normalized host records into durable model work and
// applies the results through the memory store. The engine owns prompts and
// parsing; the host supplies generation through Generator.
package jobs

import (
	"context"
	"encoding/json"
	"time"
)

// State is a job's place in its lifecycle.
type State string

const (
	StatePending   State = "pending"
	StateLeased    State = "leased"
	StateApplied   State = "applied"
	StateRetryWait State = "retry_wait"
	StateConflict  State = "conflict"
	StateCancelled State = "cancelled"
)

// Kind selects the prompt, parser, and memory write a job uses.
type Kind string

const (
	KindFact        Kind = "fact"
	KindDigest      Kind = "digest"
	KindRevision    Kind = "revision"
	KindConsolidate Kind = "consolidate"
	KindPages       Kind = "pages"
	KindGap         Kind = "gap"
)

// Job is one durable unit of capture or maintenance work.
type Job struct {
	ID         string
	Kind       Kind
	HarnessID  string
	SessionID  string
	Generation string
	Phase      string
	Scope      string
	State      State
	Lease      string
	InputHash  string
	Attempt    int
	RetryAt    time.Time
	Output     string
	Error      string
	RequestID  string
}

// HostRecord is one normalized transcript record from an adapter.
type HostRecord struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"sessionID"`
	Generation string     `json:"generation"`
	Time       time.Time  `json:"time"`
	Role       string     `json:"role"`
	Text       string     `json:"text"`
	Synthetic  bool       `json:"synthetic,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
}

// ToolCall is one host tool outcome attached to a record.
type ToolCall struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Class        string          `json:"class"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Outcome      string          `json:"outcome"`
	EditEvidence json.RawMessage `json:"editEvidence,omitempty"`
}

// RunBoundary marks a terminal run or a compaction point. Available is false
// when the host cannot supply the history for that generation.
type RunBoundary struct {
	SessionID  string    `json:"sessionID"`
	Generation string    `json:"generation"`
	Kind       string    `json:"kind"`
	Time       time.Time `json:"time"`
	Available  bool      `json:"available"`
}

// Generator is the host-supplied model call. The engine never stores
// credentials or imports a provider.
type Generator func(ctx context.Context, prompt string) (string, error)
