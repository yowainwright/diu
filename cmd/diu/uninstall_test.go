package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestUninstallProjectRemovesZshIntegrationAndPreservesData(t *testing.T) {
	config := setupTestHomeConfig(t)
	if err := config.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	setupUninstallZshFixture(t, config)
	if err := os.WriteFile(config.Storage.JSONFile, []byte("usage data"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}

	output := captureStderr(t, func() {
		if err := uninstallProject(&command{}, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "configuration and usage data preserved") {
		t.Fatalf("uninstall output = %q", output)
	}

	assertZshSourceRemoved(t)
	assertGeneratedZshIntegrationRemoved(t, config)
	assertFileContents(t, config.Storage.JSONFile, "usage data")
}

func TestRemoveZshShellIntegrationIsRepeatable(t *testing.T) {
	config := setupTestHomeConfig(t)
	if err := config.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	setupUninstallZshFixture(t, config)
	for range 2 {
		if err := removeZshShellIntegration(config); err != nil {
			t.Fatal(err)
		}
	}
	assertZshSourceRemoved(t)
	assertGeneratedZshIntegrationRemoved(t, config)
}

func setupUninstallZshFixture(t *testing.T, config *core.Config) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".zshrc")
	block := zshSourceBlock(zshIntegrationPath(config))
	content := strings.Join([]string{"# user before", "", block, "", "# user after", ""}, "\n")
	if err := os.WriteFile(path, []byte(content), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	generated := zshIntegrationGeneratedMarker + "\n# hook body\n"
	if err := os.WriteFile(zshIntegrationPath(config), []byte(generated), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
}

func assertZshSourceRemoved(t *testing.T) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".zshrc")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(contents)
	blockRemoved := !strings.Contains(content, zshSourceBlockStart)
	userContentPreserved := strings.Contains(content, "# user before") && strings.Contains(content, "# user after")
	cleanedSafely := blockRemoved && userContentPreserved
	if !cleanedSafely {
		t.Fatalf("unexpected zshrc after uninstall: %q", contents)
	}
}

func assertGeneratedZshIntegrationRemoved(t *testing.T, config *core.Config) {
	t.Helper()
	if _, err := os.Stat(zshIntegrationPath(config)); !os.IsNotExist(err) {
		t.Fatalf("generated zsh integration remains, stat error = %v", err)
	}
}
