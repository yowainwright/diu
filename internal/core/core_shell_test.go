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
	hasExpectedLines := posixContainsPath && fishContainsPath
	if !hasExpectedLines {
		t.Fatalf("shell path lines = %q, %q", posix, fish)
	}
}

func TestWrapperGuardBypassesRecordingForOrphansAndRecorderChildren(t *testing.T) {
	for _, expected := range []string{`[ ! -x "$DIU_RECORD_BINARY" ]`, `[ "${DIU_RECORDING:-}" = 1 ]`, `exec "$DIU_ORIGINAL" "$@"`} {
		if !strings.Contains(WrapperCommandGuard, expected) {
			t.Errorf("wrapper guard missing %q", expected)
		}
	}
}
