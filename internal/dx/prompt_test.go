package dx_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestInputReadsTrimmedInput(t *testing.T) {
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

func TestRequireRejectsMismatchedText(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("wrong\n"), &bytes.Buffer{})
	err := prompt.Require("Type package name", "expected")
	if !errors.Is(err, dx.ErrCancelled) {
		t.Fatalf("Require error = %v, want ErrCancelled", err)
	}
}

func TestRequireAcceptsExactText(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("expected\n"), &bytes.Buffer{})
	if err := prompt.Require("Type package name", "expected"); err != nil {
		t.Fatalf("Require failed: %v", err)
	}
}

func TestPromptOperationsPropagateInputErrors(t *testing.T) {
	operations := []func(*dx.Prompter) error{
		func(prompt *dx.Prompter) error {
			_, err := prompt.Input("Action")
			return err
		},
		func(prompt *dx.Prompter) error {
			return prompt.Require("Type go", "go")
		},
	}
	for index, operation := range operations {
		prompt := dx.NewPrompter(strings.NewReader(""), &bytes.Buffer{})
		if err := operation(prompt); !errors.Is(err, io.EOF) {
			t.Fatalf("operation %d error = %v, want EOF", index, err)
		}
	}
}
