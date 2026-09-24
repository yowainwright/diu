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

func TestSaveExistingUpdatesSymlinkedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := DefaultConfig()
	configPath, linkTarget := createSymlinkedConfig(t, config)
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	if err := config.SaveExisting(); err != nil {
		t.Fatal(err)
	}
	assertConfigSymlink(t, configPath, linkTarget)
	saved, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Monitoring.Process.ShouldAutoInstallWrappers {
		t.Fatal("existing symlinked config was not updated")
	}
}

func TestSaveExistingRejectsSymlinkOutsideConfigDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := defaultConfigPath()
	outsidePath := filepath.Join(t.TempDir(), "outside.json")
	createExternalConfigSymlink(t, configPath, outsidePath)
	if err := DefaultConfig().SaveExisting(); err == nil {
		t.Fatal("saving through an external symlink must fail")
	}
	assertFileContents(t, outsidePath, "keep this file")
}

func createSymlinkedConfig(t *testing.T, config *Config) (string, string) {
	t.Helper()
	configPath := defaultConfigPath()
	targetPath := filepath.Join(filepath.Dir(configPath), "config.data")
	linkTarget := filepath.Base(targetPath)
	if err := config.SaveTo(targetPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkTarget, configPath); err != nil {
		t.Fatal(err)
	}
	return configPath, linkTarget
}

func createExternalConfigSymlink(t *testing.T, configPath, targetPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(configPath), OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("keep this file"), PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, configPath); err != nil {
		t.Fatal(err)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Fatalf("file contents = %q; want %q", contents, want)
	}
}

func assertConfigSymlink(t *testing.T, configPath, wantTarget string) {
	t.Helper()
	target, err := os.Readlink(configPath)
	targetMatches := err == nil && target == wantTarget
	if !targetMatches {
		t.Fatalf("config symlink target = %q, %v; want %q", target, err, wantTarget)
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
