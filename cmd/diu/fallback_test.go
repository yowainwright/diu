package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

func TestRecordExecutionPreservesSlowNPMEnrichment(t *testing.T) {
	config := setupTestHomeConfig(t)
	installSlowNPMProbe(t)
	payload := `{"tool":"npm","command":"npm install -g typescript","args":["install","-g","typescript"]}`
	runRecordExecution(t, payload)
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{Tool: core.ToolNPM})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("slow enrichment recorded %d executions, want 1", len(records))
	}
	assertNoFallbackContention(t, config)
}

func assertNoFallbackContention(t *testing.T, config *core.Config) {
	t.Helper()
	_, contended, err := observability.ReadFallbackContention(config.Daemon.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if contended {
		t.Fatal("uncontended recording incorrectly reported lock contention")
	}
}

func installSlowNPMProbe(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "npm")
	script := "#!/bin/sh\nsleep 0.12\nprintf '/synthetic/npm\\n'\n"
	if err := os.WriteFile(path, []byte(script), core.OwnerExecutableMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
}
