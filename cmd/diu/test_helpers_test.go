package main

import (
	"os"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func requireConfigDirectories(t *testing.T, config *core.Config) {
	t.Helper()
	if err := config.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent; stat error = %v", path, err)
	}
}
