package main

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestRenderZshIntegrationEscapesExecutableMapEntries(t *testing.T) {
	target := executableTarget{Name: "fmt'check", Tool: core.ToolNPM, Package: "@acme/fmt'check"}
	integrationPath := "/tmp/diu's integration.zsh"
	contents, err := renderZshIntegration([]executableTarget{target}, integrationPath)
	if err != nil {
		t.Fatalf("renderZshIntegration failed: %v", err)
	}
	assertShellIntegrationContains(t, contents,
		"typeset -g _DIU_INTEGRATION_FILE="+zshSingleQuote(integrationPath),
		`_DIU_TOOL_MAP+=('fmt'\''check' '"npm"')`,
		`_DIU_PACKAGE_MAP+=('fmt'\''check' '"@acme/fmt'\''check"')`,
		`_DIU_EXECUTABLE_MAP+=('fmt'\''check' '"fmt'\''check"')`,
		"add-zsh-hook preexec _diu_preexec",
		"source \"$_DIU_INTEGRATION_FILE\"",
		"command_position=1",
		"skip_redirection_target=1",
		"record --background",
	)
}

func TestRenderZshIntegrationIncludesUnmappedCommandFallback(t *testing.T) {
	contents, err := renderZshIntegration(nil, "/unused/integration.zsh")
	if err != nil {
		t.Fatal(err)
	}
	assertShellIntegrationContains(t, contents,
		`_diu_record_unmapped "$invocation" "$prefix"`,
		`"--executable=${1:t}"`,
		`builtin whence "$lookup" -- "$1"`,
	)
}

func TestZshKrewDispatch(t *testing.T) {
	config, root := krewExecutableFixture(t)
	writeExecutableForTest(t, filepath.Join(root, "bin", "kubectl-ctx-extra"), "#!/bin/sh\nexit 0\n")
	source, err := renderZshIntegration(discoverExecutables(config), "/unused/integration.zsh")
	if err != nil {
		t.Fatal(err)
	}
	for submitted, want := range krewDispatchCases() {
		t.Run(submitted, func(t *testing.T) {
			output := parseKrewSubmitted(t, source, submitted)
			if output != want {
				t.Fatalf("recorded %q, want %q", output, want)
			}
		})
	}
}

func TestZshKrewUnderscoreNameCannotOverrideBuiltin(t *testing.T) {
	config, root := krewExecutableFixture(t)
	for _, path := range []string{"bin/kubectl-api_resources", "receipts/api-resources.yaml"} {
		if err := os.Remove(filepath.Join(root, path)); err != nil {
			t.Fatal(err)
		}
	}
	writeKrewExecutableFixture(t, root, "api_resources")
	source, err := renderZshIntegration(discoverExecutables(config), "/unused/integration.zsh")
	if err != nil {
		t.Fatal(err)
	}
	got := parseKrewSubmitted(t, source, "kubectl api-resources --help")
	if got != "generic" {
		t.Fatalf("built-in command attributed to Krew plugin: %s", got)
	}
}

func krewDispatchCases() map[string]string {
	cases := map[string]string{
		"kubectl ctx":                               `"ctx"`,
		"kubectl view-secret --help":                `"view-secret"`,
		"kubectl view_secret":                       `"view-secret"`,
		"kubectl-ctx":                               `"ctx"`,
		"KREW_ROOT=/other kubectl ctx":              "generic",
		"kubectl ctx " + strings.Repeat("arg ", 33): "generic",
		"kubectl get pods":                          "generic",
		"kubectl api-resources":                     "generic",
		"kubectl api_resources":                     `"api-resources"`,
		"kubectl --namespace test ctx":              "generic",
		"kubectl ctx extra":                         "generic",
		"kubectl ctx '$dynamic'":                    "generic",
		"kubectl ctx > output":                      `"ctx"`,
		"kubectl ctx; kubectl view-secret":          "\"ctx\"\n\"view-secret\"",
	}
	maps.Copy(cases, krewEnvironmentDispatchCases())
	return cases
}

func krewEnvironmentDispatchCases() map[string]string {
	return map[string]string{
		"KREW_ROOT=/other kubectl ctx; kubectl ctx": "generic\n\"ctx\"",
		"FOO=x true && kubectl ctx":                 "generic\n\"ctx\"",
		"FOO=x; kubectl ctx":                        `"ctx"`,
	}
}

func parseKrewSubmitted(t *testing.T, source, submitted string) string {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is unavailable")
	}
	script := "typeset -gi _DIU_IN_PREEXEC=1\n" + source
	script += "\n_diu_emit_record() { print -r -- \"$2\"; }\n_diu_record_unmapped() { print generic; }\n"
	script += "probe() { local submitted=" + zshSingleQuote(submitted) + "\n"
	script += "local command_position=1 command_prefix='' command_lookup=0 skip_prefix_argument=0 skip_redirection_target=0 loop_header=0 case_header=0 case_pattern=0 case_depth=0 exec_prefix=0 skip_exec_argument=0\n_diu_parse_submitted\n}\nprobe\ntrue\n"
	output, err := exec.CommandContext(t.Context(), zsh, "-df", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh parser failed: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestZshCaptureTimestamp(t *testing.T) {
	before := time.Now().Add(-time.Second)
	output := captureZshTimestamp(t)
	timestamp, err := time.Parse(time.RFC3339Nano, output)
	if err != nil {
		t.Fatalf("invalid launch timestamp %q: %v", output, err)
	}
	isCurrent := !timestamp.Before(before) && !timestamp.After(time.Now())
	if !isCurrent {
		t.Fatalf("launch timestamp %q is not current UTC time", output)
	}
	if len(output) != len("2026-10-14T12:00:00.000Z") {
		t.Fatalf("expected milliseconds and UTC suffix, got %q", output)
	}
}

func captureZshTimestamp(t *testing.T) string {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is unavailable")
	}
	script := "typeset -gi _DIU_IN_PREEXEC=1\n" + zshPreexecHook
	script += "\n_diu_capture_timestamp || exit 1\nprint -r -- \"$timestamp\"\n"
	command := exec.CommandContext(t.Context(), zsh, "-df", "-c", script)
	command.Env = []string{"PATH=/usr/bin:/bin", "TZ=America/Los_Angeles"}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("timestamp capture failed: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestInstallZshSourceBlockWritesThroughSymlink(t *testing.T) {
	path, target, integrationPath := createSymlinkedZshConfig(t)
	if err := installZshSourceBlock(path, integrationPath); err != nil {
		t.Fatalf("installZshSourceBlock failed for symlink: %v", err)
	}
	assertFileContents(t, target, "# dotfiles\n\n\n"+zshSourceBlock(integrationPath)+"\n")
	assertShellConfigSymlinkPreserved(t, path, target)
}

func createSymlinkedZshConfig(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	dotfiles := t.TempDir()
	path := filepath.Join(home, ".zshrc")
	target := filepath.Join(dotfiles, "zshrc")
	if err := os.WriteFile(target, []byte("# dotfiles\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	integrationPath := filepath.Join(t.TempDir(), "shell-integration.zsh")
	return path, target, integrationPath
}

func assertShellConfigSymlinkPreserved(t *testing.T, path, target string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("failed to inspect zshrc symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("zshrc symlink was replaced: info=%v", info)
	}
	info, err = os.Stat(target)
	if err != nil {
		t.Fatalf("failed to inspect dotfiles target: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("dotfiles permissions changed: info=%v", info)
	}
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

func TestRefreshZshShellIntegrationPreservesUnownedFile(t *testing.T) {
	config := core.DefaultConfig()
	config.Daemon.DataDir = t.TempDir()
	path := zshIntegrationPath(config)
	if err := os.WriteFile(path, []byte("user content\n"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := refreshZshShellIntegration(config); err == nil {
		t.Fatal("refreshZshShellIntegration replaced an unowned file")
	}
	assertFileContents(t, path, "user content\n")
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

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent; stat error = %v", path, err)
	}
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
