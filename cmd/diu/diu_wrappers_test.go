package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestPackageNameForExecutable(t *testing.T) {
	for _, tt := range packageNameForExecutableCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := packageNameForExecutable(tt.tool, tt.path, tt.cmd); got != tt.want {
				t.Errorf("packageNameForExecutable(%q, %q, %q) = %q, want %q", tt.tool, tt.path, tt.cmd, got, tt.want)
			}
		})
	}
}

func TestShouldSkipExecutableWrapper(t *testing.T) {
	for command, expected := range shouldSkipExecutableWrapperCases {
		if got := shouldSkipExecutableWrapper(command); got != expected {
			t.Errorf("shouldSkipExecutableWrapper(%q) = %v, want %v", command, got, expected)
		}
	}
}

func TestInstallExecutableWrappersWritesScripts(t *testing.T) {
	config := setupTestHomeConfig(t)
	wrapperDir, originalPath := configureExecutableWrapperInstall(t, config)

	targets := discoverExecutableWrappers(config)
	if len(targets) != 1 {
		t.Fatalf("Expected one wrapper target, got %#v", targets)
	}
	if targets[0].Package != "jq" {
		t.Fatalf("Package = %s, want jq", targets[0].Package)
	}

	if err := installExecutableWrappers(config); err != nil {
		t.Fatalf("installExecutableWrappers failed: %v", err)
	}
	assertInstalledWrapperScript(t, config, wrapperDir, originalPath)
}

func TestDiscoverExecutableWrappersForAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	configureAdditionalWrapperDiscovery(t, config)

	targets := discoverExecutableWrappers(config)
	assertAdditionalWrapperTargets(t, targets)
}

func TestDiscoverExecutableWrappersSkipsDisabledWatchPaths(t *testing.T) {
	config := setupTestHomeConfig(t)

	uvDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(uvDir, "ruff"), "#!/bin/bash\nexit 0\n")
	config.Monitoring.EnabledTools = []string{core.ToolPip}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolUV: {uvDir},
	}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")

	if targets := discoverExecutableWrappers(config); len(targets) != 0 {
		t.Fatalf("Expected disabled uv watch path to be ignored, got %#v", targets)
	}
}

func TestRefreshWrappersFindsNewAndRemovedTools(t *testing.T) {
	config := setupTestHomeConfig(t)
	binDir := configureExecutableWrapperScan(t, config)
	config.Monitoring.Process.ShouldAutoInstallWrappers = true
	requireConfigDirectories(t, config)
	runWrapperRefreshForTest(t, config)
	assertFileExists(t, filepath.Join(config.Monitoring.Process.WrapperDir, "jq"))
	writeExecutableForTest(t, filepath.Join(binDir, "rg"), "#!/bin/sh\nexit 0\n")
	if err := os.Remove(filepath.Join(binDir, "jq")); err != nil {
		t.Fatal(err)
	}
	runWrapperRefreshForTest(t, config)
	assertFileMissing(t, filepath.Join(config.Monitoring.Process.WrapperDir, "jq"))
	assertFileExists(t, filepath.Join(config.Monitoring.Process.WrapperDir, "rg"))
}

func TestMissingToolCleanupPreservesCustomWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	path := filepath.Join(config.Monitoring.Process.WrapperDir, "custom")
	writeExecutableForTest(t, path, "#!/bin/sh\nexit 0\n")
	if err := removeMissingToolWrappers(config.Monitoring.Process.WrapperDir); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, path)
}

func TestWrapperOriginalRoundTripsShellCharacters(t *testing.T) {
	config := setupTestHomeConfig(t)
	original := filepath.Join(t.TempDir(), "a $path `with` \"quotes\" \\ slashes")
	target := executableWrapper{Name: "test", OriginalPath: original, Tool: "npm", Package: "test"}
	content := executableWrapperScript(config, target)
	if got := generatedWrapperOriginal(content); got != original {
		t.Fatalf("wrapper original = %q, want %q", got, original)
	}
}
