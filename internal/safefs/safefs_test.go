package safefs

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	writeTestFile(t, path, "hello")
	assertStatFile(t, path)
	assertLstatFile(t, path)
	assertReadFile(t, path, "hello")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func assertStatFile(t *testing.T, path string) {
	t.Helper()

	info, err := Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	hasExpectedName := info.Name() == "file.txt"
	isFile := !info.IsDir()
	statOK := hasExpectedName && isFile
	if !statOK {
		t.Fatalf("Unexpected Stat result: name=%q isDir=%v", info.Name(), info.IsDir())
	}
}

func assertLstatFile(t *testing.T, path string) {
	t.Helper()

	lstatInfo, err := Lstat(path)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if lstatInfo.Name() != "file.txt" {
		t.Fatalf("Unexpected Lstat name: %q", lstatInfo.Name())
	}
}

func assertReadFile(t *testing.T, path, expected string) {
	t.Helper()

	data, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != expected {
		t.Fatalf("ReadFile = %q, want %s", data, expected)
	}
}

func TestEmptyPathValidation(t *testing.T) {
	tests := map[string]func(string) error{
		"Stat": func(path string) error {
			_, err := Stat(path)
			return err
		},
		"Lstat": func(path string) error {
			_, err := Lstat(path)
			return err
		},
		"ReadFile": func(path string) error {
			_, err := ReadFile(path)
			return err
		},
		"OpenFile": func(path string) error {
			file, err := OpenFile(path, os.O_RDONLY, 0)
			if file != nil {
				_ = file.Close()
			}
			return err
		},
	}

	for name, fn := range tests {
		assertEmptyPathValidation(t, name, "", fn)
		assertEmptyPathValidation(t, name, "   ", fn)
	}
}

func assertEmptyPathValidation(t *testing.T, name, path string, fn func(string) error) {
	t.Helper()
	t.Run(name+"/"+path, func(t *testing.T) {
		err := fn(path)
		if err == nil {
			t.Fatal("Expected empty path to fail")
		}
		if !strings.Contains(err.Error(), "path cannot be empty") {
			t.Fatalf("error = %v, want empty path error", err)
		}
	})
}

func TestStatFilesystemRoot(t *testing.T) {
	info, err := Stat(string(filepath.Separator))
	if err != nil {
		t.Fatalf("Stat filesystem root failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("filesystem root IsDir = false")
	}
}

func TestLstatDoesNotFollowSymlink(t *testing.T) {
	link := createTestSymlink(t)
	assertLstatSeesSymlink(t, link)
	assertStatFollowsSymlink(t, link)
}

func createTestSymlink(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Skipf("Symlink unavailable: %v", err)
	}
	return link
}

func assertLstatSeesSymlink(t *testing.T, link string) {
	t.Helper()
	info, err := Lstat(link)
	if err != nil {
		t.Fatalf("Lstat symlink failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Lstat mode = %v, want symlink", info.Mode())
	}
}

func assertStatFollowsSymlink(t *testing.T, link string) {
	t.Helper()
	info, err := Stat(link)
	if err != nil {
		t.Fatalf("Stat symlink failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Stat mode = %v, should follow symlink", info.Mode())
	}
}

func TestOpenFileReportsOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")

	file, err := OpenFile(path, os.O_RDONLY, 0)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("Expected missing file to fail")
	}
}

func TestSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content.txt")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	digest, err := SHA256(path)
	if err != nil {
		t.Fatalf("SHA256 failed: %v", err)
	}
	expectedBytes := sha256.Sum256(content)
	expected := hex.EncodeToString(expectedBytes[:])
	if digest != expected {
		t.Fatalf("SHA256 = %q, want %q", digest, expected)
	}
	if _, err := SHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("SHA256 accepted missing file")
	}
}
