package dx_test

import (
	"bytes"
	"errors"
	"io"
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

func TestTerminalPrompterRejectsAutomation(t *testing.T) {
	t.Setenv("CI", "1")

	prompt, err := dx.TerminalPrompter()
	rejectedPrompt := prompt == nil
	hasNonInteractiveError := errors.Is(err, dx.ErrNonInteractive)
	rejectedAutomation := rejectedPrompt && hasNonInteractiveError
	if !rejectedAutomation {
		t.Fatalf("TerminalPrompter = %#v, %v", prompt, err)
	}
}

func TestPromptUsesSafeConfirmationDefault(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	var output bytes.Buffer
	prompt := dx.NewPrompter(strings.NewReader("\n"), &output)

	defaultConfirmed := false
	confirmed, err := prompt.Confirm("Remove package", defaultConfirmed)
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

func TestPromptParsesConfirmationAnswers(t *testing.T) {
	for _, test := range confirmationAnswerTests() {
		assertConfirmationAnswer(t, test)
	}
}

type confirmationAnswerTest struct {
	input        string
	shouldAccept bool
	hasError     bool
}

func confirmationAnswerTests() []confirmationAnswerTest {
	return []confirmationAnswerTest{
		{input: "yes\n", shouldAccept: true},
		{input: "Y\n", shouldAccept: true},
		{input: "no\n", shouldAccept: false},
		{input: "N\n", shouldAccept: false},
		{input: "maybe\n", hasError: true},
	}
}

func assertConfirmationAnswer(t *testing.T, test confirmationAnswerTest) {
	t.Helper()

	prompt := dx.NewPrompter(strings.NewReader(test.input), &bytes.Buffer{})
	defaultConfirmed := false
	confirmed, err := prompt.Confirm("Continue", defaultConfirmed)
	if (err != nil) != test.hasError {
		t.Fatalf("Confirm(%q) error = %v", test.input, err)
	}
	if err != nil {
		return
	}
	if confirmed != test.shouldAccept {
		t.Fatalf("Confirm(%q) = %v, want %v", test.input, confirmed, test.shouldAccept)
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
	hasValue := value == "go"
	hasPrompt := output.String() == "? Tool [go]: "
	inputOK := hasValue && hasPrompt
	if !inputOK {
		t.Fatalf("InputDefault = %q, %q", value, output.String())
	}
}

func TestPromptKeepsExplicitInputOverDefault(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("npm\n"), &bytes.Buffer{})

	value, err := prompt.InputDefault("Tool", "go")
	if err != nil {
		t.Fatalf("InputDefault failed: %v", err)
	}
	if value != "npm" {
		t.Fatalf("InputDefault = %q, want npm", value)
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

func TestPromptRejectsInvalidSelections(t *testing.T) {
	choices := []dx.Choice{{Label: "Go", Value: "go"}}
	for _, input := range []string{"0\n", "2\n", "go\n"} {
		prompt := dx.NewPrompter(strings.NewReader(input), &bytes.Buffer{})
		if _, err := prompt.Select("Tool", choices); err == nil {
			t.Fatalf("Select(%q) succeeded", input)
		}
	}
	prompt := dx.NewPrompter(strings.NewReader("1\n"), &bytes.Buffer{})
	if _, err := prompt.Select("Tool", nil); err == nil {
		t.Fatal("Select without choices succeeded")
	}
}

func TestPromptRequiresExactDestructiveConfirmation(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("wrong\n"), &bytes.Buffer{})
	err := prompt.Require("Type package name", "expected")
	if !errors.Is(err, dx.ErrCancelled) {
		t.Fatalf("Require error = %v, want ErrCancelled", err)
	}
}

func TestPromptAcceptsExactRequiredText(t *testing.T) {
	prompt := dx.NewPrompter(strings.NewReader("expected\n"), &bytes.Buffer{})
	if err := prompt.Require("Type package name", "expected"); err != nil {
		t.Fatalf("Require failed: %v", err)
	}
}

func TestPromptOperationsPropagateInputErrors(t *testing.T) {
	choices := []dx.Choice{{Label: "Go", Value: "go"}}
	operations := []func(*dx.Prompter) error{
		func(prompt *dx.Prompter) error {
			_, err := prompt.InputDefault("Tool", "go")
			return err
		},
		func(prompt *dx.Prompter) error {
			defaultConfirmed := false
			_, err := prompt.Confirm("Continue", defaultConfirmed)
			return err
		},
		func(prompt *dx.Prompter) error {
			_, err := prompt.Select("Tool", choices)
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
