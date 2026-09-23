package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveExistingDoesNotCreateMissingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := DefaultConfig()
	if err := config.SaveExisting(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(defaultConfigPath())); !os.IsNotExist(err) {
		t.Fatalf("saving an absent config created its directory: %v", err)
	}
}

func TestSaveExistingDoesNotRecreateRemovedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := DefaultConfig()
	if err := config.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(defaultConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveExisting(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("removed config was recreated: %v", err)
	}
}

func TestSaveExistingPersistsChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := DefaultConfig()
	if err := config.Save(); err != nil {
		t.Fatal(err)
	}
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	if err := config.SaveExisting(); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Monitoring.Process.ShouldAutoInstallWrappers {
		t.Fatal("existing config was not updated")
	}
}

func TestSaveExistingReportsWriteFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(defaultConfigPath(), OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	if err := config.SaveExisting(); err == nil {
		t.Fatal("saving over a directory must report failure")
	}
}
