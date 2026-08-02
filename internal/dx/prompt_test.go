package dx_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestPromptReadsTrimmedInput(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	var output bytes.Buffer
	prompt := dx.NewPrompter(strings.NewReader("  value  \n"), &output)

	value, err := prompt.Input("Name")
	if err != nil {
		t.Fatalf("Input failed: %v", err)
	}
	if value != "value" {
		t.Fatalf("value = %q, want value", value)
	}
	if output.String() != "? Name: " {
		t.Fatalf("output = %q, want prompt", output.String())
	}
}

func TestPromptUsesSafeConfirmationDefault(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	var output bytes.Buffer
	prompt := dx.NewPrompter(strings.NewReader("\n"), &output)

	confirmed, err := prompt.Confirm("Remove package", false)
	if err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}
	if confirmed {
		t.Fatal("blank confirmation accepted destructive action")
	}
	if output.String() != "? Remove package [y/N]: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestPromptUsesInputDefault(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	var output bytes.Buffer
	prompt := dx.NewPrompter(strings.NewReader("\n"), &output)

	value, err := prompt.InputDefault("Tool", "go")
	if err != nil {
		t.Fatalf("InputDefault failed: %v", err)
	}
	if value != "go" || output.String() != "? Tool [go]: " {
		t.Fatalf("InputDefault = %q, %q", value, output.String())
	}
}

func TestPromptReturnsSelectedValue(t *testing.T) {
	var output bytes.Buffer
	prompt := dx.NewPrompter(strings.NewReader("2\n"), &output)
	choices := []dx.Choice{{Label: "npm", Value: "npm"}, {Label: "Go", Value: "go"}}

	selected, err := prompt.Select("Package manager", choices)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if selected != "go" {
		t.Fatalf("selected = %q, want go", selected)
	}
}

func TestPromptRequiresExactDestructiveConfirmation(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("wrong\n"), &bytes.Buffer{})
	err := prompt.Require("Type package name", "expected")
	if !errors.Is(err, dx.ErrCancelled) {
		t.Fatalf("Require error = %v, want ErrCancelled", err)
	}
}
