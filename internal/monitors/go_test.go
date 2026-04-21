package monitors

import (
	"reflect"
	"testing"
)

func TestGoExtractPackages(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	packages := monitor.extractGoPackages([]string{
		"golang.org/x/tools/cmd/stringer@latest",
		"./...",
		"-v",
		"github.com/cli/cli/v2/cmd/gh",
		".",
	})

	expected := []string{"stringer", "gh"}
	if !reflect.DeepEqual(packages, expected) {
		t.Fatalf("expected %v, got %v", expected, packages)
	}
}

func TestExtractVersionToken(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "go version output",
			output: "go version go1.25.9 darwin/arm64",
			want:   "go1.25.9",
		},
		{
			name:   "generic version output",
			output: "fake-go-tool version v0.9.0",
			want:   "v0.9.0",
		},
		{
			name:   "missing version token",
			output: "build metadata only",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersionToken(tt.output)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
