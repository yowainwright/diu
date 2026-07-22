package main

import "testing"

func TestFlagSetParsesLongAndShortFlags(t *testing.T) {
	flags := newFlagSet()
	var tool string
	var limit int
	var yes bool
	flags.StringVarP(&tool, "tool", "t", "", "tool")
	flags.IntVarP(&limit, "limit", "n", 20, "limit")
	flags.BoolVarP(&yes, "yes", "y", false, "yes")

	args, err := flags.parse([]string{"--tool", "npm", "-n", "5", "-y", "package"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if tool != "npm" {
		t.Fatalf("tool = %q, want npm", tool)
	}
	if limit != 5 {
		t.Fatalf("limit = %d, want 5", limit)
	}
	if !yes {
		t.Fatal("yes = false, want true")
	}
	if len(args) != 1 || args[0] != "package" {
		t.Fatalf("args = %#v, want [package]", args)
	}
}

func TestFlagSetParsesEqualsValues(t *testing.T) {
	flags := newFlagSet()
	var format string
	var enabled bool
	flags.StringVar(&format, "format", "table", "format")
	flags.BoolVar(&enabled, "enabled", true, "enabled")

	args, err := flags.parse([]string{"--format=json", "--enabled=false"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
	if format != "json" {
		t.Fatalf("format = %q, want json", format)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
}

func TestFlagSetParsesAttachedShortValue(t *testing.T) {
	flags := newFlagSet()
	var limit int
	flags.IntVarP(&limit, "limit", "n", 20, "limit")

	args, err := flags.parse([]string{"-n5", "package"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if limit != 5 {
		t.Fatalf("limit = %d, want 5", limit)
	}
	if len(args) != 1 || args[0] != "package" {
		t.Fatalf("args = %#v, want [package]", args)
	}
}

func TestCommandDispatchesToSubcommand(t *testing.T) {
	var gotArgs []string
	root := &command{Use: "diu"}
	child := &command{
		Use: "query",
		RunE: func(cmd *command, args []string) error {
			gotArgs = args
			return nil
		},
	}
	root.AddCommand(child)

	if err := root.Execute([]string{"query", "one", "two"}); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "one" || gotArgs[1] != "two" {
		t.Fatalf("gotArgs = %#v, want [one two]", gotArgs)
	}
}

func TestRootCommandIncludesUninstall(t *testing.T) {
	uninstallCmd := newRootCommand().findCommand("uninstall")
	if uninstallCmd == nil {
		t.Fatal("Expected root command to include uninstall")
	}
	if uninstallCmd.RunE == nil {
		t.Fatal("Expected uninstall command to be executable")
	}
}

func TestFlagSetVisitOnlyChangedFlags(t *testing.T) {
	flags := newFlagSet()
	var tool string
	var limit int
	flags.StringVarP(&tool, "tool", "t", "", "tool")
	flags.IntVarP(&limit, "limit", "n", 20, "limit")

	if _, err := flags.parse([]string{"--tool", "go"}); err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	var visited []string
	flags.Visit(func(flag *flag) {
		visited = append(visited, flag.Name)
	})
	if len(visited) != 1 || visited[0] != "tool" {
		t.Fatalf("visited = %#v, want [tool]", visited)
	}
}
