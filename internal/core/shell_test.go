package core

import (
	"strings"
	"testing"
)

func TestShellPathLines(t *testing.T) {
	wrapperDir := "/tmp/diu"
	posix := PosixPathLine(wrapperDir)
	fish := FishPathLine(wrapperDir)
	posixContainsPath := strings.Contains(posix, wrapperDir+":$PATH")
	fishContainsPath := strings.Count(fish, wrapperDir) == 2
	if !posixContainsPath || !fishContainsPath {
		t.Fatalf("shell path lines = %q, %q", posix, fish)
	}
}
