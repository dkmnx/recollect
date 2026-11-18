package rpc

// audit: behavior - storage errors return a stable path-free message.
// audit: regression - an append failure would leak absolute host paths to the adapter.
// audit: coverage gap - error mapping had no direct test.
// audit: seam - the test calls the production mapper with a wrapped OS-style error.

import (
	"fmt"
	"testing"
)

func TestEngineFailureHidesPaths(t *testing.T) {
	err := fmt.Errorf("engine: create C:\\Users\\softw\\secret\\.engine: boom")
	got := engineFailure(err)
	if got == nil || got.Code != CodeStorage {
		t.Fatalf("engineFailure = %+v, want storage code", got)
	}
	if got.Message != "storage_failure" {
		t.Fatalf("message = %q, want storage_failure", got.Message)
	}
}

func TestJobsFailureHidesPaths(t *testing.T) {
	err := fmt.Errorf("jobs: open C:\\Users\\softw\\secret\\.engine: boom")
	got := jobsFailure(err)
	if got == nil || got.Code != CodeStorage || got.Message != "storage_failure" {
		t.Fatalf("jobsFailure = %+v, want stable storage error", got)
	}
}
