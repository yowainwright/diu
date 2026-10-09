package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestRenderZshIntegrationEscapesExecutableMapEntries(t *testing.T) {
	target := executableTarget{Name: "fmt'check", Tool: core.ToolNPM, Package: "@acme/fmt'check"}
	contents, err := renderZshIntegration([]executableTarget{target})
	if err != nil {
		t.Fatalf("renderZshIntegration failed: %v", err)
	}
	assertShellIntegrationContains(t, contents,
		`_DIU_TOOL_MAP+=('fmt'\''check' '"npm"')`,
		`_DIU_PACKAGE_MAP+=('fmt'\''check' '"@acme/fmt'\''check"')`,
		`_DIU_EXECUTABLE_MAP+=('fmt'\''check' '"fmt'\''check"')`,
		"add-zsh-hook preexec _diu_preexec",
		"record --background",
	)
}

func assertShellIntegrationContains(t *testing.T, contents string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(contents, value) {
			t.Errorf("generated zsh integration is missing %q", value)
		}
	}
}

func TestInstallZshSourceBlockReplacesDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	integrationPath := filepath.Join(t.TempDir(), "diu's integration.zsh")
	block := zshSourceBlock(integrationPath)
	initial := strings.Join([]string{"# user before", block, "# user middle", block, "# user after"}, "\n")
	if err := os.WriteFile(path, []byte(initial), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	installZshSourceBlockTwice(t, path, integrationPath)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleZshSourceBlock(t, string(contents), integrationPath)
}

func installZshSourceBlockTwice(t *testing.T, path, integrationPath string) {
	t.Helper()
	for range 2 {
		if err := installZshSourceBlock(path, integrationPath); err != nil {
			t.Fatalf("installZshSourceBlock failed: %v", err)
		}
	}
}

func assertSingleZshSourceBlock(t *testing.T, contents, integrationPath string) {
	t.Helper()
	if count := strings.Count(contents, zshSourceBlockStart); count != 1 {
		t.Fatalf("source block count = %d, want 1", count)
	}
	assertShellIntegrationContains(t, contents,
		"# user before", "# user middle", "# user after",
		"source "+zshSingleQuote(integrationPath),
	)
}

func TestRemoveMarkedShellBlocksPreservesUserContent(t *testing.T) {
	block := zshSourceBlock("/data/diu/shell-integration.zsh")
	content := strings.Join([]string{"before", block, "middle", block, "after"}, "\n")
	updated, err := removeMarkedShellBlocks(content)
	if err != nil {
		t.Fatalf("removeMarkedShellBlocks failed: %v", err)
	}
	if updated != "before\nmiddle\nafter" {
		t.Fatalf("remaining shell config = %q", updated)
	}
	if _, err := removeMarkedShellBlocks(zshSourceBlockStart); err == nil {
		t.Fatal("malformed DIU source block was accepted")
	}
}

func TestRemoveGeneratedZshIntegrationPreservesUnownedFile(t *testing.T) {
	config := core.DefaultConfig()
	config.Daemon.DataDir = t.TempDir()
	path := zshIntegrationPath(config)
	if err := os.WriteFile(path, []byte("user content\n"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := removeGeneratedZshIntegration(config); err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, path, "user content\n")
	if err := os.WriteFile(path, []byte(zshIntegrationGeneratedMarker+"\ngenerated\n"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := removeGeneratedZshIntegration(config); err != nil {
		t.Fatal(err)
	}
	assertShellIntegrationMissing(t, path)
}

func assertFileContents(t *testing.T, path, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != expected {
		t.Fatalf("file contents = %q, want %q", contents, expected)
	}
}

func assertShellIntegrationMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("integration file stat error = %v, want not exist", err)
	}
}
