package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestRunRecordCountsPackageOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := &cobra.Command{}
	cmd.Flags().String("tool", "", "")
	cmd.Flags().Int("exit-code", 0, "")
	if err := cmd.Flags().Set("tool", "brew"); err != nil {
		t.Fatalf("failed to set tool flag: %v", err)
	}
	if err := cmd.Flags().Set("exit-code", "0"); err != nil {
		t.Fatalf("failed to set exit-code flag: %v", err)
	}

	if err := runRecord(cmd, []string{"install", "wget"}); err != nil {
		t.Fatalf("runRecord failed: %v", err)
	}

	config, err := core.LoadConfig("")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}
	defer store.Close()

	pkg, err := store.GetPackage("homebrew", "wget")
	if err != nil {
		t.Fatalf("failed to get package: %v", err)
	}
	if pkg.UsageCount != 1 {
		t.Fatalf("expected usage count 1, got %d", pkg.UsageCount)
	}

	stats, err := store.GetStatistics()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.TotalExecutions != 1 {
		t.Fatalf("expected 1 execution, got %d", stats.TotalExecutions)
	}
}

func TestNormalizeToolName(t *testing.T) {
	tests := map[string]string{
		"brew":          "homebrew",
		"homebrew-cask": "homebrew",
		"go-binary":     "go",
		"pip3":          "pip",
		"npm":           "npm",
	}

	for input, want := range tests {
		if got := normalizeToolName(input); got != want {
			t.Fatalf("normalizeToolName(%q) = %q, want %q", input, got, want)
		}
	}
}
