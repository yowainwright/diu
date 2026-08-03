package dx_test

import (
	"bytes"
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
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("arguments = %#v, want [one two]", got)
	}
}

func TestCommandParsesSupportedFlagForms(t *testing.T) {
	var remaining []string
	command := &dx.Command{
		Use: "check",
		RunE: func(_ *dx.Command, args []string) error {
			remaining = args
			return nil
		},
	}
	var tool string
	var limit int
	var enabled bool
	command.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	command.Flags().IntVarP(&limit, "limit", "n", 20, "limit")
	command.Flags().BoolVar(&enabled, "enabled", true, "enabled")

	if err := command.Execute([]string{"-tnpm", "-n5", "--enabled=false"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if tool != "npm" || limit != 5 || enabled {
		t.Fatalf("flags = %q, %d, %v", tool, limit, enabled)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining arguments = %#v, want none", remaining)
	}
}

func TestCommandKeepsHelpAndErrorsOnSeparateStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := &dx.Command{Use: "diu", Short: "DIU CLI", Output: &stdout, ErrorOutput: &stderr}

	if err := root.Execute([]string{"--help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "DIU CLI") || stderr.Len() != 0 {
		t.Fatalf("help streams = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	if err := root.Execute([]string{"unknown"}); err == nil {
		t.Fatal("unknown command succeeded")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage: diu") {
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
	var stdout bytes.Buffer
	root := &dx.Command{Use: "diu", Output: &stdout}
	child := &dx.Command{Use: "query [search]", Long: "Query execution history"}
	var limit int
	child.Flags().IntVarP(&limit, "limit", "n", 20, "Limit results")
	child.AddCommand(&dx.Command{Use: "hidden", Hidden: true})
	root.AddCommand(child)

	if err := root.Execute([]string{"help", "query"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	want := []string{
		"Query execution history",
		"Usage: diu query [search]",
		"-n, --limit",
		"Limit results",
	}
	for _, text := range want {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("help = %q, want %q", stdout.String(), text)
		}
	}
	if strings.Contains(stdout.String(), "hidden") {
		t.Fatalf("help exposed hidden command: %q", stdout.String())
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
	if len(visited) != 1 || visited[0] != "tool" {
		t.Fatalf("visited = %#v, want [tool]", visited)
	}
}

func TestFlagSetReturnsParsedValues(t *testing.T) {
	command := &dx.Command{}
	var tool string
	var limit int
	var enabled bool
	command.Flags().StringVar(&tool, "tool", "", "tool")
	command.Flags().IntVar(&limit, "limit", 20, "limit")
	command.Flags().BoolVar(&enabled, "enabled", false, "enabled")

	if _, err := command.Flags().Parse([]string{"--tool=go", "--limit=5", "--enabled"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	gotTool, toolErr := command.Flags().GetString("tool")
	gotLimit, limitErr := command.Flags().GetInt("limit")
	gotEnabled, enabledErr := command.Flags().GetBool("enabled")
	if toolErr != nil || limitErr != nil || enabledErr != nil {
		t.Fatalf("getters failed: %v, %v, %v", toolErr, limitErr, enabledErr)
	}
	if gotTool != "go" || gotLimit != 5 || !gotEnabled {
		t.Fatalf("values = %q, %d, %v", gotTool, gotLimit, gotEnabled)
	}
	if flag := command.Flag("limit"); flag == nil || flag.Value.String() != "5" {
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
	flags := dx.NewFlagSet()
	var tool string
	var limit int
	var yes bool
	flags.StringVarP(&tool, "tool", "t", "", "tool")
	flags.IntVarP(&limit, "limit", "n", 20, "limit")
	flags.BoolVarP(&yes, "yes", "y", false, "yes")

	remaining, err := flags.Parse([]string{"--tool", "npm", "-n", "5", "-y", "package", "--", "--literal"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if tool != "npm" || limit != 5 || !yes {
		t.Fatalf("flags = %q, %d, %v", tool, limit, yes)
	}
	if len(remaining) != 2 || remaining[0] != "package" || remaining[1] != "--literal" {
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
