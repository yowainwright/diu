package dx_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestCommandDispatchesArguments(t *testing.T) {
	var got []string
	root := &dx.Command{Use: "diu"}
	child := &dx.Command{
		Use: "query",
		RunE: func(_ *dx.Command, args []string) error {
			got = args
			return nil
		},
	}
	root.AddCommand(child)

	if err := root.Execute([]string{"query", "one", "two"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	expected := []string{"one", "two"}
	if !slices.Equal(got, expected) {
		t.Fatalf("arguments = %#v, want [one two]", got)
	}
}

func TestCommandParsesSupportedFlagForms(t *testing.T) {
	state := &supportedFlagState{}
	command := supportedFlagCommand(state)
	if err := command.Execute([]string{"-tnpm", "-n5", "--enabled=false"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	assertSupportedFlags(t, state)
}

type supportedFlagState struct {
	tool      string
	limit     int
	enabled   bool
	remaining []string
}

func supportedFlagCommand(state *supportedFlagState) *dx.Command {
	command := &dx.Command{
		Use: "check",
		RunE: func(_ *dx.Command, args []string) error {
			state.remaining = args
			return nil
		},
	}
	command.Flags().StringVarP(&state.tool, "tool", "t", "", "tool")
	command.Flags().IntVarP(&state.limit, "limit", "n", 20, "limit")
	enabledDefault := true
	command.Flags().BoolVar(&state.enabled, "enabled", enabledDefault, "enabled")
	return command
}

func assertSupportedFlags(t *testing.T, state *supportedFlagState) {
	t.Helper()

	gotTool := state.tool == "npm"
	gotLimit := state.limit == 5
	gotDisabled := !state.enabled
	gotExpectedFlags := gotTool && gotLimit && gotDisabled
	if !gotExpectedFlags {
		t.Fatalf("flags = %q, %d, %v", state.tool, state.limit, state.enabled)
	}
	if len(state.remaining) != 0 {
		t.Fatalf("remaining arguments = %#v, want none", state.remaining)
	}
}

func TestCommandKeepsHelpAndErrorsOnSeparateStreams(t *testing.T) {
	root, stdout, stderr := commandStreams()
	assertCommandHelpStreams(t, root, stdout, stderr)
	assertCommandErrorStreams(t, root, stdout, stderr)
}

func commandStreams() (*dx.Command, *bytes.Buffer, *bytes.Buffer) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := &dx.Command{Use: "diu", Short: "DIU CLI", Output: &stdout, ErrorOutput: &stderr}
	return root, &stdout, &stderr
}

func assertCommandHelpStreams(t *testing.T, root *dx.Command, stdout, stderr *bytes.Buffer) {
	t.Helper()
	if err := root.Execute([]string{"--help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	stdoutText := stdout.String()
	hasHelp := strings.Contains(stdoutText, "DIU CLI")
	cleanStderr := stderr.Len() == 0
	helpStreamsOK := hasHelp && cleanStderr
	if !helpStreamsOK {
		t.Fatalf("help streams = stdout %q, stderr %q", stdoutText, stderr.String())
	}
}

func assertCommandErrorStreams(t *testing.T, root *dx.Command, stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout.Reset()
	if err := root.Execute([]string{"unknown"}); err == nil {
		t.Fatal("unknown command succeeded")
	}
	cleanStdout := stdout.Len() == 0
	hasUsage := strings.Contains(stderr.String(), "Usage: diu")
	errorStreamsOK := cleanStdout && hasUsage
	if !errorStreamsOK {
		t.Fatalf("error streams = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestCommandPrintsProvidedVersion(t *testing.T) {
	var stdout bytes.Buffer
	root := &dx.Command{
		Use:     "diu",
		Version: func() string { return "diu 1.2.3" },
		Output:  &stdout,
	}

	if err := root.Execute([]string{"--version"}); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if stdout.String() != "diu 1.2.3\n" {
		t.Fatalf("version = %q", stdout.String())
	}
}

func TestCommandHelpDescribesNestedCommand(t *testing.T) {
	root, stdout := nestedHelpCommand()
	if err := root.Execute([]string{"help", "query"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	assertNestedHelp(t, stdout.String())
}

func nestedHelpCommand() (*dx.Command, *bytes.Buffer) {
	var stdout bytes.Buffer
	root := &dx.Command{Use: "diu", Output: &stdout}
	child := &dx.Command{Use: "query [search]", Long: "Query execution history"}
	var limit int
	child.Flags().IntVarP(&limit, "limit", "n", 20, "Limit results")
	child.AddCommand(&dx.Command{Use: "hidden", Hidden: true})
	root.AddCommand(child)
	return root, &stdout
}

func assertNestedHelp(t *testing.T, output string) {
	t.Helper()
	want := []string{
		"Query execution history",
		"Usage: diu query [search]",
		"-n, --limit",
		"Limit results",
	}
	for _, text := range want {
		if !strings.Contains(output, text) {
			t.Fatalf("help = %q, want %q", output, text)
		}
	}
	if strings.Contains(output, "hidden") {
		t.Fatalf("help exposed hidden command: %q", output)
	}
}

func TestFlagVisitReportsOnlyChangedValues(t *testing.T) {
	flags := dx.NewFlagSet()
	var tool string
	var limit int
	flags.StringVar(&tool, "tool", "", "tool")
	flags.IntVar(&limit, "limit", 20, "limit")
	if _, err := flags.Parse([]string{"--tool", "go"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var visited []string
	flags.Visit(func(flag *dx.Flag) { visited = append(visited, flag.Name) })
	expected := []string{"tool"}
	if !slices.Equal(visited, expected) {
		t.Fatalf("visited = %#v, want [tool]", visited)
	}
}

func TestFlagSetReturnsParsedValues(t *testing.T) {
	command := parsedValuesCommand()
	if _, err := command.Flags().Parse([]string{"--tool=go", "--limit=5", "--enabled"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	assertParsedFlagValues(t, command)
	assertLimitFlag(t, command)
}

func parsedValuesCommand() *dx.Command {
	command := &dx.Command{}
	var tool string
	var limit int
	var enabled bool
	command.Flags().StringVar(&tool, "tool", "", "tool")
	command.Flags().IntVar(&limit, "limit", 20, "limit")
	enabledDefault := false
	command.Flags().BoolVar(&enabled, "enabled", enabledDefault, "enabled")
	return command
}

func assertParsedFlagValues(t *testing.T, command *dx.Command) {
	t.Helper()
	gotTool, toolErr := command.Flags().GetString("tool")
	gotLimit, limitErr := command.Flags().GetInt("limit")
	gotEnabled, enabledErr := command.Flags().GetBool("enabled")
	gotToolValue := toolErr == nil
	gotLimitValue := limitErr == nil
	gotEnabledValue := enabledErr == nil
	gotValues := gotToolValue && gotLimitValue && gotEnabledValue
	if !gotValues {
		t.Fatalf("getters failed: %v, %v, %v", toolErr, limitErr, enabledErr)
	}
	gotExpectedValues := gotTool == "go" && gotLimit == 5 && gotEnabled
	if !gotExpectedValues {
		t.Fatalf("values = %q, %d, %v", gotTool, gotLimit, gotEnabled)
	}
}

func assertLimitFlag(t *testing.T, command *dx.Command) {
	t.Helper()
	flag := command.Flag("limit")
	hasLimitFlag := flag != nil && flag.Value.String() == "5"
	if !hasLimitFlag {
		t.Fatalf("limit flag = %#v", flag)
	}
}

func TestFlagSetRejectsMismatchedGetters(t *testing.T) {
	flags := dx.NewFlagSet()
	var tool string
	flags.StringVar(&tool, "tool", "go", "tool")

	if _, err := flags.GetString("missing"); err == nil {
		t.Fatal("missing string flag succeeded")
	}
	if _, err := flags.GetInt("tool"); err == nil {
		t.Fatal("string flag returned as int")
	}
	if _, err := flags.GetBool("tool"); err == nil {
		t.Fatal("string flag returned as bool")
	}
}

func TestFlagSetParsesSeparatedValuesAndTerminator(t *testing.T) {
	state := &separatedFlagState{}
	flags := separatedValueFlags(state)
	remaining, err := flags.Parse([]string{"--tool", "npm", "-n", "5", "-y", "package", "--", "--literal"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	assertSeparatedFlags(t, state, remaining)
}

type separatedFlagState struct {
	tool  string
	limit int
	yes   bool
}

func separatedValueFlags(state *separatedFlagState) *dx.FlagSet {
	flags := dx.NewFlagSet()
	flags.StringVarP(&state.tool, "tool", "t", "", "tool")
	flags.IntVarP(&state.limit, "limit", "n", 20, "limit")
	yesDefault := false
	flags.BoolVarP(&state.yes, "yes", "y", yesDefault, "yes")
	return flags
}

func assertSeparatedFlags(t *testing.T, state *separatedFlagState, remaining []string) {
	t.Helper()

	gotTool := state.tool == "npm"
	gotLimit := state.limit == 5
	gotExpectedFlags := gotTool && gotLimit && state.yes
	if !gotExpectedFlags {
		t.Fatalf("flags = %q, %d, %v", state.tool, state.limit, state.yes)
	}
	expectedRemaining := []string{"package", "--literal"}
	if !slices.Equal(remaining, expectedRemaining) {
		t.Fatalf("remaining = %#v", remaining)
	}
}

func TestFlagSetRejectsInvalidFlags(t *testing.T) {
	flags := dx.NewFlagSet()
	var limit int
	flags.IntVar(&limit, "limit", 20, "limit")

	if _, err := flags.Parse([]string{"--unknown"}); err == nil {
		t.Fatal("unknown flag succeeded")
	}
	if _, err := flags.Parse([]string{"--limit", "nope"}); err == nil {
		t.Fatal("invalid integer succeeded")
	}
}
