// Package rpc serves the Recollect JSON-RPC 2.0 protocol over stdio.
package rpc

import "encoding/json"

// Request is one newline-delimited JSON-RPC frame.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Response is one newline-delimited JSON-RPC reply.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a stable JSON-RPC failure.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Stable JSON-RPC error codes.
const (
	CodeInvalidRequest = -32600
	CodeUnknownMethod  = -32601
	CodeInvalidParams  = -32602
	CodeProtocol       = -32001
	CodeReadRequired   = -32002
	CodeRevision       = -32003
	CodeBusy           = -32004
	CodeConflict       = -32005
	CodeStorage        = -32006
)

// Protocol is the Recollect protocol version negotiated at initialize.
type Protocol struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

const protocolMajor = 1
const jsonRPCVersion = "2.0"

func failure(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

func invalidParams(message string) *Error {
	return failure(CodeInvalidParams, message)
}
