package monitors

import (
	"os"
	"path/filepath"
	"testing"
)

func prependFakeCommand(t *testing.T, name, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("Failed to write fake %s command: %v", name, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("Failed to chmod fake %s command: %v", name, err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}
