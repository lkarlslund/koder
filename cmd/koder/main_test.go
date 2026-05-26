package main

import (
	"fmt"
	"testing"
)

func TestExitCodeForErrorMapsProcessRestart(t *testing.T) {
	code, ok := exitCodeForError(fmt.Errorf("wrapped: %w", errProcessRestart))
	if !ok {
		t.Fatal("expected process restart error to map to an exit code")
	}
	if code != processRestartExitCode {
		t.Fatalf("expected exit code %d, got %d", processRestartExitCode, code)
	}
}

func TestExitCodeForErrorIgnoresGenericError(t *testing.T) {
	code, ok := exitCodeForError(fmt.Errorf("boom"))
	if ok {
		t.Fatalf("expected generic error not to map to a special code, got %d", code)
	}
}
