package core

import (
	"strings"
	"testing"
)

func TestWrapperRecordingKeepsCommandAndRecorderIsolated(t *testing.T) {
	script := WrapperRecordingScript("    \"metadata\": {}\n")
	for _, expected := range recordingIsolationParts {
		if !strings.Contains(script, expected) {
			t.Errorf("recording script missing %q", expected)
		}
	}
	lookup := `DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"`
	if strings.Count(WrapperCommandGuard+script, lookup) != 1 {
		t.Fatal("recorder executable must be resolved exactly once")
	}
}

var recordingIsolationParts = []string{
	`"$DIU_ORIGINAL" "$@"`,
	"EXIT_CODE=$?",
	"START_TIME=$(/bin/date",
	"payload=$(/bin/cat",
	"/usr/bin/whoami",
	"/usr/bin/nc -w 1 -U",
	`DIU_RECORDING=1 "$DIU_RECORD_BINARY" record`,
	"} </dev/null >/dev/null 2>&1 &",
	"exit $EXIT_CODE",
}
