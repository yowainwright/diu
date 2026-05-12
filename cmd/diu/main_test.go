package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestPackageNameForExecutable(t *testing.T) {
	const (
		homebrewExecutable = "/opt/homebrew/Cellar/jq/1.8.1/bin/jq"
		npmExecutable      = "/opt/homebrew/lib/node_modules/@scope/tool/bin/tool"
		goExecutable       = "/Users/test/go/bin/golangci-lint"
		homebrewCommand    = "jq"
		npmCommand         = "tool"
		goCommand          = "golangci-lint"
		homebrewPackage    = "jq"
		npmPackage         = "@scope/tool"
	)

	tests := []struct {
		name string
		tool string
		path string
		cmd  string
		want string
	}{
		{
			name: "homebrew cellar path",
			tool: core.ToolHomebrew,
			path: filepath.Clean(homebrewExecutable),
			cmd:  homebrewCommand,
			want: homebrewPackage,
		},
		{
			name: "npm scoped package path",
			tool: core.ToolNPM,
			path: filepath.Clean(npmExecutable),
			cmd:  npmCommand,
			want: npmPackage,
		},
		{
			name: "go binary fallback",
			tool: core.ToolGo,
			path: filepath.Clean(goExecutable),
			cmd:  goCommand,
			want: goCommand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := packageNameForExecutable(tt.tool, tt.path, tt.cmd); got != tt.want {
				t.Errorf("packageNameForExecutable(%q, %q, %q) = %q, want %q", tt.tool, tt.path, tt.cmd, got, tt.want)
			}
		})
	}
}

func TestShouldSkipExecutableWrapper(t *testing.T) {
	const (
		hiddenCommand = ".hidden"
		diuCommand    = "diu"
		brewCommand   = "brew"
		normalCommand = "jq"
		emptyCommand  = ""
		skipExpected  = true
		trackExpected = false
	)

	tests := map[string]bool{
		emptyCommand:  skipExpected,
		hiddenCommand: skipExpected,
		diuCommand:    skipExpected,
		brewCommand:   skipExpected,
		normalCommand: trackExpected,
	}

	for command, expected := range tests {
		if got := shouldSkipExecutableWrapper(command); got != expected {
			t.Errorf("shouldSkipExecutableWrapper(%q) = %v, want %v", command, got, expected)
		}
	}
}

func TestFilterPackagesSearchAndUnused(t *testing.T) {
	const (
		searchQuery          = "jq"
		unusedDuration       = "24h"
		usedPackageName      = "jq"
		otherPackageName     = "ripgrep"
		usedPackageCount     = 3
		unusedPackageCount   = 0
		expectedPackageCount = 1
	)

	packages := []*core.PackageInfo{
		{
			Name:       usedPackageName,
			Tool:       core.ToolHomebrew,
			UsageCount: usedPackageCount,
			LastUsed:   time.Now(),
		},
		{
			Name:       otherPackageName,
			Tool:       core.ToolHomebrew,
			UsageCount: unusedPackageCount,
		},
	}

	filtered, err := filterPackages(packages, packageListOptions{Search: searchQuery})
	if err != nil {
		t.Fatalf("filterPackages failed: %v", err)
	}
	if len(filtered) != expectedPackageCount || filtered[0].Name != usedPackageName {
		t.Fatalf("Expected only %s, got %v", usedPackageName, filtered)
	}

	filtered, err = filterPackages(packages, packageListOptions{Unused: unusedDuration})
	if err != nil {
		t.Fatalf("filterPackages unused failed: %v", err)
	}
	if len(filtered) != expectedPackageCount || filtered[0].Name != otherPackageName {
		t.Fatalf("Expected only %s, got %v", otherPackageName, filtered)
	}
}

func TestUninstallPlan(t *testing.T) {
	const (
		homebrewPackage = "jq"
		npmPackage      = "eslint"
		goPackage       = "golangci-lint"
		goPath          = "/Users/test/go/bin/golangci-lint"
	)

	tests := []struct {
		name string
		pkg  *core.PackageInfo
		want []string
	}{
		{
			name: "homebrew",
			pkg:  &core.PackageInfo{Name: homebrewPackage, Tool: core.ToolHomebrew},
			want: []string{homebrewCommandName, uninstallSubcommand, homebrewPackage},
		},
		{
			name: "npm",
			pkg:  &core.PackageInfo{Name: npmPackage, Tool: core.ToolNPM},
			want: []string{npmCommandName, uninstallSubcommand, npmGlobalFlag, npmPackage},
		},
		{
			name: "go executable",
			pkg:  &core.PackageInfo{Name: goPackage, Tool: core.ToolGo, Path: goPath},
			want: []string{removeFilePlan},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uninstallPlan(tt.pkg)
			if err != nil {
				t.Fatalf("uninstallPlan failed: %v", err)
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("uninstallPlan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePackageManagerName(t *testing.T) {
	const (
		homebrewPackage       = "ripgrep"
		scopedNPMPackage      = "@scope/tool"
		tappedHomebrewPackage = "owner/tap/tool"
		flagLikePackage       = "--help"
		traversalPackage      = "../tool"
		shellPackage          = "tool;rm"
	)

	validPackages := []string{
		homebrewPackage,
		scopedNPMPackage,
		tappedHomebrewPackage,
	}
	for _, name := range validPackages {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err != nil {
				t.Fatalf("validatePackageManagerName(%q) failed: %v", name, err)
			}
		})
	}

	invalidPackages := []string{
		flagLikePackage,
		traversalPackage,
		shellPackage,
	}
	for _, name := range invalidPackages {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err == nil {
				t.Fatalf("validatePackageManagerName(%q) should fail", name)
			}
		})
	}
}

func TestValidateRemovableExecutablePath(t *testing.T) {
	const (
		executableName    = "tool"
		nonExecutableName = "notes.txt"
		executableScript  = "#!/bin/bash\nexit 0\n"
		plainTextContent  = "not executable\n"
	)

	tempDir := t.TempDir()
	executablePath := filepath.Join(tempDir, executableName)
	if err := os.WriteFile(executablePath, []byte(executableScript), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write executable: %v", err)
	}
	if err := os.Chmod(executablePath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark executable: %v", err)
	}

	validated, err := validateRemovableExecutablePath(executablePath)
	if err != nil {
		t.Fatalf("validateRemovableExecutablePath failed: %v", err)
	}
	if validated != executablePath {
		t.Errorf("validateRemovableExecutablePath() = %s, want %s", validated, executablePath)
	}

	nonExecutablePath := filepath.Join(tempDir, nonExecutableName)
	if err := os.WriteFile(nonExecutablePath, []byte(plainTextContent), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	if _, err := validateRemovableExecutablePath(nonExecutablePath); err == nil {
		t.Fatal("Expected non-executable path validation to fail")
	}
}
