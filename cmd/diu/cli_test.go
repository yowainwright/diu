package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestNewUninstallCommand(t *testing.T) {
	uninstallCmd := newUninstallCommand()
	if uninstallCmd.Use != "uninstall" {
		t.Fatalf("Use = %q, want uninstall", uninstallCmd.Use)
	}
	if uninstallCmd.RunE == nil {
		t.Fatal("Expected uninstall command to be executable")
	}
}

func TestExitStatusPreservesCommandExitCode(t *testing.T) {
	commandErr := &dx.CommandError{Name: "tool", Code: 7, Err: errors.New("failed")}
	if got := exitStatus(fmt.Errorf("wrapped: %w", commandErr)); got != 7 {
		t.Fatalf("exitStatus = %d, want 7", got)
	}
	if got := exitStatus(errors.New("failed")); got != 1 {
		t.Fatalf("fallback exitStatus = %d, want 1", got)
	}
}
