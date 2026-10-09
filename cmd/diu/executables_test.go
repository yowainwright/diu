package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestPackageNameForExecutable(t *testing.T) {
	for _, test := range packageNameForExecutableCases {
		t.Run(test.name, func(t *testing.T) {
			if got := packageNameForExecutable(test.tool, test.path, test.cmd); got != test.want {
				t.Fatalf("packageNameForExecutable(%q, %q) = %q, want %q", test.tool, test.path, got, test.want)
			}
		})
	}
}

func TestShouldSkipExecutable(t *testing.T) {
	for name, want := range map[string]bool{
		"": true, ".hidden": true, "diu": true, "brew": true,
		"npm": true, "pip3": true, "jq": false,
	} {
		if got := shouldSkipExecutable(name); got != want {
			t.Errorf("shouldSkipExecutable(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDiscoverExecutablesUsesConfiguredToolsAndPATHPriority(t *testing.T) {
	config, preferredDir := configuredExecutableDiscovery(t)
	targets := discoverExecutables(config)
	assertPreferredExecutable(t, targets, preferredDir)
}

func configuredExecutableDiscovery(t *testing.T) (*core.Config, string) {
	t.Helper()
	preferredDir := executableFixtureDir(t, "jq")
	managedDir := executableFixtureDir(t, "jq")
	t.Setenv("PATH", preferredDir+string(os.PathListSeparator)+managedDir)
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {managedDir, preferredDir},
	}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	return config, preferredDir
}

func assertPreferredExecutable(t *testing.T, targets []executableTarget, preferredDir string) {
	t.Helper()
	if len(targets) != 1 {
		t.Fatalf("executable targets = %#v, want one", targets)
	}
	target := targets[0]
	nameMatches := target.Name == "jq"
	toolAndPackageMatch := target.Tool == core.ToolHomebrew && target.Package == "jq"
	identityMatches := nameMatches && toolAndPackageMatch
	if !identityMatches {
		t.Fatalf("executable target = %#v", targets[0])
	}
	wantPath := filepath.Join(preferredDir, "jq")
	if target.OriginalPath != wantPath {
		t.Fatalf("selected executable = %q, want PATH-preferred %q", target.OriginalPath, wantPath)
	}
}

func TestDiscoverExecutablesSkipsDisabledToolsAndInvalidEntries(t *testing.T) {
	binDir := executableFixtureDir(t, "jq")
	if err := os.WriteFile(filepath.Join(binDir, "notes"), []byte("not executable"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = nil
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{core.ToolHomebrew: {binDir}}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")

	if targets := discoverExecutables(config); len(targets) != 0 {
		t.Fatalf("disabled tool targets = %#v, want none", targets)
	}
}

func TestCurrentShellHomeDirsRejectsRelativeHome(t *testing.T) {
	t.Setenv("HOME", "relative-home")
	if _, err := currentShellHomeDirs(); err == nil {
		t.Fatal("currentShellHomeDirs accepted a relative HOME")
	}
}

func TestCompareExecutableTargetsSortsByName(t *testing.T) {
	first := executableTarget{Name: "alpha"}
	second := executableTarget{Name: "beta"}
	if got := compareExecutableTargets(first, second); got >= 0 {
		t.Fatalf("target comparison = %d, want alpha before beta", got)
	}
}

func TestAddGoExecutableDirsUsesConfiguredGoBin(t *testing.T) {
	dir := executableFixtureDir(t, "go-tool")
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{core.ToolGo}
	config.Tools.Go.GoBin = dir
	targets := make(map[string]executableTarget)
	addGoExecutableDirs(config, targets)
	assertExecutableTargetPresent(t, targets, "go-tool", core.ToolGoBinary)
}

func TestAddGoExecutableDirsSkipsDisabledTool(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = nil
	targets := make(map[string]executableTarget)
	addGoExecutableDirs(config, targets)
	if len(targets) != 0 {
		t.Fatalf("disabled Go executable targets = %#v", targets)
	}
}

func TestAddExecutableEntrySkipsIgnoredAndManagerMismatch(t *testing.T) {
	dir := executableFixtureDir(t, "jq")
	cellarDir := filepath.Join(t.TempDir(), "Cellar", "jq", "1", "bin")
	if err := os.MkdirAll(cellarDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutableForTest(t, filepath.Join(cellarDir, "npm-tool"), "#!/bin/sh\nexit 0\n")
	targets := make(map[string]executableTarget)
	addExecutableEntry(targets, core.ToolHomebrew, dir, "brew")
	addExecutableEntry(targets, core.ToolNPM, cellarDir, "npm-tool")
	if len(targets) != 0 {
		t.Fatalf("ignored or mismatched executable targets = %#v", targets)
	}
}

func TestAddExecutableEntryKeepsEarlierPATHTarget(t *testing.T) {
	preferredDir := executableFixtureDir(t, "jq")
	managedDir := executableFixtureDir(t, "jq")
	t.Setenv("PATH", preferredDir+string(os.PathListSeparator)+managedDir)
	preferredPath := filepath.Join(preferredDir, "jq")
	targets := map[string]executableTarget{"jq": {Name: "jq", OriginalPath: preferredPath}}
	addExecutableEntry(targets, core.ToolHomebrew, managedDir, "jq")
	if targets["jq"].OriginalPath != preferredPath {
		t.Fatalf("preferred executable changed: %#v", targets["jq"])
	}
}

func TestExecutablePathPriorityFallsAfterPATHEntries(t *testing.T) {
	paths := []string{t.TempDir(), t.TempDir()}
	t.Setenv("PATH", strings.Join(paths, string(os.PathListSeparator)))
	if got := executablePathPriority(filepath.Join(t.TempDir(), "tool")); got != len(paths) {
		t.Fatalf("executablePathPriority = %d, want %d", got, len(paths))
	}
}

func TestUsableExecutablePathRejectsMissingDirectoriesAndFiles(t *testing.T) {
	directory := t.TempDir()
	nonExecutable := filepath.Join(directory, "notes")
	if err := os.WriteFile(nonExecutable, []byte("notes"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing")
	shouldBeUnusable := false
	assertExecutablePathUsable(t, directory, shouldBeUnusable)
	assertExecutablePathUsable(t, nonExecutable, shouldBeUnusable)
	assertExecutablePathUsable(t, missing, shouldBeUnusable)
}

func assertExecutableTargetPresent(t *testing.T, targets map[string]executableTarget, name, tool string) {
	t.Helper()
	target, exists := targets[name]
	targetMissing := !exists
	toolDoesNotMatch := target.Tool != tool
	invalidTarget := targetMissing || toolDoesNotMatch
	if invalidTarget {
		t.Fatalf("executable target %q = %#v, exists=%v", name, target, exists)
	}
}

func assertExecutablePathUsable(t *testing.T, path string, shouldBeUsable bool) {
	t.Helper()
	if got := usableExecutablePath(path); got != shouldBeUsable {
		t.Fatalf("usableExecutablePath(%q) = %v, want %v", path, got, shouldBeUsable)
	}
}

func executableFixtureDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
	return dir
}
