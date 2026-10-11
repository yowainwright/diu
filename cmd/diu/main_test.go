package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

func TestCLICommandHelpFormsMatch(t *testing.T) {
	for _, path := range [][]string{{"query"}, {"check"}, {"stats"}, {"status"}, {"packages"}, {"config", "get"}} {
		helpArgs := append([]string{"help"}, path...)
		flagArgs := append(append([]string{}, path...), "--help")
		help := commandHelpOutput(t, helpArgs)
		flagHelp := commandHelpOutput(t, flagArgs)
		if help != flagHelp {
			t.Fatalf("help forms differ for %v", path)
		}
	}
}

func commandHelpOutput(t *testing.T, args []string) string {
	t.Helper()
	var output bytes.Buffer
	root := newRootCommand()
	root.Output = &output
	if err := root.Execute(args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("missing usage: %q", output.String())
	}
	return output.String()
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
	assertPublicCommandsListed(t, stdout.String())
	assertStyleguideFlagListed(t, stdout.String())
	assertRecordCommandHidden(t, stdout.String())
	assertEmptyHelpStderr(t, stderr.String())
}

func TestRootCommandNoArgsPrintsUsage(t *testing.T) {
	var stdout bytes.Buffer
	root := newRootCommand()
	root.Output = &stdout
	if err := root.Execute(nil); err != nil {
		t.Fatalf("root command failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: diu") {
		t.Fatalf("root output = %q", stdout.String())
	}
}

func TestRootCommandUnknownPrintsUsageToStderr(t *testing.T) {
	var stderr bytes.Buffer
	root := newRootCommand()
	root.ErrorOutput = &stderr
	if err := root.Execute([]string{"wat"}); err == nil {
		t.Fatal("unknown command succeeded")
	}
	if !strings.Contains(stderr.String(), "Usage: diu") {
		t.Fatalf("root stderr = %q", stderr.String())
	}
}

func assertStyleguideFlagListed(t *testing.T, help string) {
	t.Helper()

	if !strings.Contains(help, "--styleguide") {
		t.Fatalf("help = %q, want --styleguide flag", help)
	}
}

func assertPublicCommandsListed(t *testing.T, help string) {
	t.Helper()

	for _, name := range []string{"daemon", "query", "stats", "packages", "check", "manage", "config", "status", "diagnostics", "setup", "uninstall", "scan"} {
		if !strings.Contains(help, name) {
			t.Fatalf("help = %q, want command %q", help, name)
		}
	}
}

func assertRecordCommandHidden(t *testing.T, help string) {
	t.Helper()

	if strings.Contains(help, "record") {
		t.Fatalf("help exposed hidden record command: %q", help)
	}
}

func assertEmptyHelpStderr(t *testing.T, stderr string) {
	t.Helper()

	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMain(m *testing.M) {
	os.Exit(runWithIsolatedHome(m))
}

func runWithIsolatedHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "diu-command-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
