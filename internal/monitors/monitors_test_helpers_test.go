package monitors

import (
	"os"
	"path/filepath"
	"testing"
)

func prependFakeCommand(t *testing.T, name, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := writeFakeCommand(t, binDir, name, script)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func setOnlyFakeCommand(t *testing.T, name, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := writeFakeCommand(t, binDir, name, script)
	t.Setenv("PATH", binDir)
	return path
}

func writeFakeCommand(t *testing.T, binDir, name, script string) string {
	t.Helper()

	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("Failed to write fake %s command: %v", name, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("Failed to chmod fake %s command: %v", name, err)
	}
	return path
}
