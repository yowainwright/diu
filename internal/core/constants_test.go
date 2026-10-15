package core

import (
	"path/filepath"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name     string
		linked   string
		module   string
		expected string
	}{
		{name: "linked release", linked: "1.2.3", module: "v9.9.9", expected: "1.2.3"},
		{name: "module release", linked: "dev", module: "v1.2.3", expected: "1.2.3"},
		{name: "module prerelease", linked: "dev", module: "v1.2.3-rc.1", expected: "1.2.3-rc.1"},
		{name: "local source", linked: "dev", module: "(devel)", expected: "dev"},
		{name: "missing versions", linked: "", module: "", expected: "dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveVersion(test.linked, test.module); got != test.expected {
				t.Fatalf("resolveVersion(%q, %q) = %q, want %q", test.linked, test.module, got, test.expected)
			}
		})
	}
}

func TestCurrentVersionUsesLinkedVersion(t *testing.T) {
	original := Version
	Version = "4.5.6"
	t.Cleanup(func() { Version = original })

	if got := CurrentVersion(); got != "4.5.6" {
		t.Fatalf("CurrentVersion() = %q, want 4.5.6", got)
	}
}

func TestRecorderSlotPathUsesStableSlotName(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "recorder-3.lock")
	if got := RecorderSlotPath(dir, 3); got != want {
		t.Fatalf("recorder slot path = %q, want %q", got, want)
	}
}

func TestNormalizeToolName(t *testing.T) {
	for input, expected := range normalizeToolNameCases() {
		if got := NormalizeToolName(input); got != expected {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func normalizeToolNameCases() map[string]string {
	return map[string]string{
		"brew":       ToolHomebrew,
		"Homebrew":   ToolHomebrew,
		"golang":     ToolGo,
		" npm ":      ToolNPM,
		"pip3":       ToolPip,
		"python3":    ToolPip,
		"executable": ToolExecutable,
	}
}
