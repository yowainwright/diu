package main

import (
	"os"
	"path/filepath"
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

func executableFixtureDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
	return dir
}
