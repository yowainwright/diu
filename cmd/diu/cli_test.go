package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
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

func TestRootCommandHelpListsPublicCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := newRootCommand()
	root.Output = &stdout
	root.ErrorOutput = &stderr

	if err := root.Execute([]string{"--help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	for _, name := range []string{"daemon", "query", "stats", "packages", "check", "manage", "config", "status", "diagnostics", "setup", "uninstall", "scan"} {
		if !strings.Contains(stdout.String(), name) {
			t.Fatalf("help = %q, want command %q", stdout.String(), name)
		}
	}
	if strings.Contains(stdout.String(), "record") {
		t.Fatalf("help exposed hidden record command: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
