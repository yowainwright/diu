// Package utils provides common utility functions for path and filesystem operations.
package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath ensures a path is absolute, clean, and valid.
// Returns an error if the path is invalid, empty, or relative.
func ValidatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Check for path traversal in the original path
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return fmt.Errorf("path contains unsafe traversal: %s", path)
		}
	}

	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	return nil
}

// EnsureDirectory creates a directory with the specified permissions if it doesn't exist.
// Returns an error if directory creation fails.
func EnsureDirectory(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// ValidateExecutable checks if a path points to an executable file.
// Returns the cleaned path and an error if validation fails.
func ValidateExecutable(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}

	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
	}

	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", clean, err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not an executable: %s", clean)
	}

	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("path is not executable: %s", clean)
	}

	return clean, nil
}

// ValidateRemovablePath checks if a path can be safely removed.
// Returns the cleaned path and an error if validation fails.
func ValidateRemovablePath(path string) (string, error) {
	validated, err := ValidateExecutable(path)
	if err != nil {
		return "", err
	}

	// Additional safety check: ensure we're not removing system directories
	for _, segment := range strings.Split(validated, "/") {
		if segment == ".." {
			return "", fmt.Errorf("refusing to remove path with traversal: %s", path)
		}
	}

	return validated, nil
}

// SanitizeFileName sanitizes a string to be safe for use as a filename.
// Removes or replaces unsafe characters.
func SanitizeFileName(name string) string {
	if name == "" {
		return "unnamed"
	}

	// Replace unsafe characters with underscores
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune("@._+- ", r) {
			return r
		}
		if r >= 'a' && r <= 'z' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r
		}
		if r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)

	// Trim leading/trailing dots and spaces
	safe = strings.Trim(safe, " .")
	if safe == "" {
		return "unnamed"
	}

	return safe
}

// JoinPath safely joins path segments and ensures the result is clean.
func JoinPath(elements ...string) string {
	var nonEmpty []string
	for _, elem := range elements {
		trimmed := strings.TrimSpace(elem)
		if trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}

	if len(nonEmpty) == 0 {
		return ""
	}

	return filepath.Clean(filepath.Join(nonEmpty...))
}

// RelativePath returns the relative path from base to target.
// Returns an error if the paths cannot be made relative.
func RelativePath(base, target string) (string, error) {
	cleanBase := filepath.Clean(base)
	cleanTarget := filepath.Clean(target)

	rel, err := filepath.Rel(cleanBase, cleanTarget)
	if err != nil {
		return "", fmt.Errorf("cannot get relative path from %s to %s: %w", cleanBase, cleanTarget, err)
	}

	// Ensure the relative path doesn't escape the base directory
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("relative path escapes base directory: %s", rel)
	}

	return rel, nil
}

// PathExists checks if a path exists (file or directory).
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDirectory checks if a path exists and is a directory.
func IsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// IsFile checks if a path exists and is a regular file.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}
