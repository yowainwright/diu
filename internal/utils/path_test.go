package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"relative", "relative/path", true},
		{"relative with dots", "./path", true},
		{"absolute", "/usr/local/bin", false},
		{"absolute with trailing slash", "/usr/local/bin/", false},
		{"traversal", "/usr/local/../etc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestEnsureDirectory(t *testing.T) {
	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "test", "nested", "dir")

	err := EnsureDirectory(testDir, 0o755)
	if err != nil {
		t.Fatalf("EnsureDirectory failed: %v", err)
	}

	info, err := os.Stat(testDir)
	if err != nil {
		t.Fatalf("Directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("Path is not a directory")
	}
}

func TestValidateExecutable(t *testing.T) {
	tempDir := t.TempDir()

	// Test non-existent file
	_, err := ValidateExecutable("/nonexistent/path")
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	// Test directory
	err = os.MkdirAll(filepath.Join(tempDir, "dir"), 0o755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	_, err = ValidateExecutable(filepath.Join(tempDir, "dir"))
	if err == nil {
		t.Fatal("Expected error for directory")
	}

	// Test non-executable file
	nonExec := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(nonExec, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	_, err = ValidateExecutable(nonExec)
	if err == nil {
		t.Fatal("Expected error for non-executable file")
	}

	// Test valid executable
	execPath := filepath.Join(tempDir, "exec")
	if err := os.WriteFile(execPath, []byte("#!/bin/bash\nexit 0"), 0o755); err != nil {
		t.Fatalf("Failed to create executable: %v", err)
	}
	validated, err := ValidateExecutable(execPath)
	if err != nil {
		t.Fatalf("ValidateExecutable failed on valid file: %v", err)
	}
	if validated != execPath {
		t.Errorf("Expected %s, got %s", execPath, validated)
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "unnamed"},
		{"valid-name", "valid-name"},
		{"name with spaces", "name with spaces"},
		{"name/with/slashes", "name_with_slashes"},
		{".hidden", "hidden"},
		{"..hidden..", "hidden"},
		{"name:with:colons", "name_with_colons"},
		{"name*with*stars", "name_with_stars"},
		{"name?with?questions", "name_with_questions"},
		{"@npm/package", "@npm_package"},
		{"name.with.dots", "name.with.dots"},
		{"name+with+plus", "name+with+plus"},
		{"name-with-dashes", "name-with-dashes"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeFileName(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFileName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestJoinPath(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"/usr/local"}, "/usr/local"},
		{"multiple", []string{"/usr", "local", "bin"}, "/usr/local/bin"},
		{"with spaces", []string{"/usr", " local ", "bin"}, "/usr/local/bin"},
		{"with empty", []string{"/usr", "", "local", "", "bin"}, "/usr/local/bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JoinPath(tt.input...)
			if got != tt.expected {
				t.Errorf("JoinPath(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRelativePath(t *testing.T) {
	tempDir := t.TempDir()
	base := filepath.Join(tempDir, "base")
	target := filepath.Join(tempDir, "base", "sub", "file.txt")

	// Create directories
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test valid relative path
	rel, err := RelativePath(base, target)
	if err != nil {
		t.Fatalf("RelativePath failed: %v", err)
	}
	if rel != "sub/file.txt" {
		t.Errorf("RelativePath(%s, %s) = %q, want %q", base, target, rel, "sub/file.txt")
	}

	// Test escaping base directory
	outside := "/etc/passwd"
	_, err = RelativePath(base, outside)
	if err == nil {
		t.Fatal("Expected error for path outside base directory")
	}
}

func TestPathExists(t *testing.T) {
	tempDir := t.TempDir()
	existent := filepath.Join(tempDir, "exists")
	nonExistent := filepath.Join(tempDir, "does-not-exist")

	// Create a file
	if err := os.WriteFile(existent, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if !PathExists(existent) {
		t.Fatal("PathExists should return true for existent file")
	}
	if PathExists(nonExistent) {
		t.Fatal("PathExists should return false for non-existent file")
	}
}

func TestIsDirectory(t *testing.T) {
	tempDir := t.TempDir()
	dirPath := filepath.Join(tempDir, "dir")
	filePath := filepath.Join(tempDir, "file")

	// Create directory
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	// Create file
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if !IsDirectory(dirPath) {
		t.Fatal("IsDirectory should return true for directory")
	}
	if IsDirectory(filePath) {
		t.Fatal("IsDirectory should return false for file")
	}
	if IsDirectory("/nonexistent") {
		t.Fatal("IsDirectory should return false for non-existent path")
	}
}

func TestIsFile(t *testing.T) {
	tempDir := t.TempDir()
	dirPath := filepath.Join(tempDir, "dir")
	filePath := filepath.Join(tempDir, "file")

	// Create directory
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	// Create file
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if !IsFile(filePath) {
		t.Fatal("IsFile should return true for file")
	}
	if IsFile(dirPath) {
		t.Fatal("IsFile should return false for directory")
	}
	if IsFile("/nonexistent") {
		t.Fatal("IsFile should return false for non-existent path")
	}
}
