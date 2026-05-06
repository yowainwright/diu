package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/yowainwright/diu/internal/core"
)

type command struct {
	Use    string
	Short  string
	Long   string
	Hidden bool
	RunE   func(*command, []string) error

	parent   *command
	flags    *flagSet
	commands []*command
}

func (c *command) AddCommand(commands ...*command) {
	for _, child := range commands {
		child.parent = c
		c.commands = append(c.commands, child)
	}
}

func (c *command) Execute(args []string) error {
	return c.execute(args)
}

func (c *command) execute(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help":
			return c.printHelp(args[1:])
		case "-h", "--help":
			c.printUsage()
			return nil
		case "-v", "--version":
			if c.parent == nil {
				fmt.Println(coreVersion())
				return nil
			}
		}

		if child := c.findCommand(args[0]); child != nil {
			return child.execute(args[1:])
		}
	}

	remaining, err := c.Flags().parse(args)
	if err != nil {
		return err
	}
	for _, arg := range remaining {
		if arg == "-h" || arg == "--help" {
			c.printUsage()
			return nil
		}
	}

	if c.RunE == nil {
		if len(remaining) > 0 {
			c.printUsageTo(os.Stderr)
			return fmt.Errorf("unknown command: %s", remaining[0])
		}
		c.printUsage()
		return nil
	}
	return c.RunE(c, remaining)
}

func (c *command) Flags() *flagSet {
	if c.flags == nil {
		c.flags = newFlagSet()
	}
	return c.flags
}

func (c *command) Flag(name string) *flag {
	return c.Flags().lookupLong(name)
}

func (c *command) findCommand(name string) *command {
	for _, child := range c.commands {
		if commandName(child.Use) == name {
			return child
		}
	}
	return nil
}

func (c *command) printHelp(args []string) error {
	if len(args) == 0 {
		c.printUsage()
		return nil
	}
	child := c.findCommand(args[0])
	if child == nil {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	return child.printHelp(args[1:])
}

func (c *command) printUsage() {
	c.printUsageTo(os.Stdout)
}

func (c *command) printUsageTo(w io.Writer) {
	if c.Long != "" {
		fmt.Fprintln(w, c.Long)
	} else if c.Short != "" {
		fmt.Fprintln(w, c.Short)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Usage: %s\n", c.usagePath())

	if len(c.commands) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Commands:")
		for _, child := range c.commands {
			if child.Hidden {
				continue
			}
			fmt.Fprintf(w, "  %-14s %s\n", commandName(child.Use), child.Short)
		}
	}

	if c.flags != nil && len(c.flags.order) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Flags:")
		for _, flag := range c.flags.order {
			short := ""
			if flag.short != "" {
				short = "-" + flag.short + ", "
			}
			fmt.Fprintf(w, "  %s--%-16s %s\n", short, flag.name, flag.usage)
		}
	}
}

func (c *command) usagePath() string {
	var parts []string
	for current := c; current != nil; current = current.parent {
		parts = append([]string{current.Use}, parts...)
	}
	return strings.Join(parts, " ")
}

func commandName(use string) string {
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func coreVersion() string {
	return core.Version
}

type flagSet struct {
	byName  map[string]*flag
	byShort map[string]*flag
	order   []*flag
}

func newFlagSet() *flagSet {
	return &flagSet{
		byName:  make(map[string]*flag),
		byShort: make(map[string]*flag),
	}
}

func (s *flagSet) StringVarP(target *string, name, shorthand, value, usage string) {
	*target = value
	s.add(&flag{name: name, short: shorthand, usage: usage, kind: flagKindString, stringValue: target})
}

func (s *flagSet) StringVar(target *string, name, value, usage string) {
	s.StringVarP(target, name, "", value, usage)
}

func (s *flagSet) IntVarP(target *int, name, shorthand string, value int, usage string) {
	*target = value
	s.add(&flag{name: name, short: shorthand, usage: usage, kind: flagKindInt, intValue: target})
}

func (s *flagSet) IntVar(target *int, name string, value int, usage string) {
	s.IntVarP(target, name, "", value, usage)
}

func (s *flagSet) BoolVarP(target *bool, name, shorthand string, value bool, usage string) {
	*target = value
	s.add(&flag{name: name, short: shorthand, usage: usage, kind: flagKindBool, boolValue: target})
}

func (s *flagSet) BoolVar(target *bool, name string, value bool, usage string) {
	s.BoolVarP(target, name, "", value, usage)
}

func (s *flagSet) GetString(name string) (string, error) {
	flag := s.lookupLong(name)
	if flag == nil || flag.kind != flagKindString {
		return "", fmt.Errorf("unknown string flag: %s", name)
	}
	return *flag.stringValue, nil
}

func (s *flagSet) GetInt(name string) (int, error) {
	flag := s.lookupLong(name)
	if flag == nil || flag.kind != flagKindInt {
		return 0, fmt.Errorf("unknown int flag: %s", name)
	}
	return *flag.intValue, nil
}

func (s *flagSet) GetBool(name string) (bool, error) {
	flag := s.lookupLong(name)
	if flag == nil || flag.kind != flagKindBool {
		return false, fmt.Errorf("unknown bool flag: %s", name)
	}
	return *flag.boolValue, nil
}

func (s *flagSet) Visit(fn func(*flag)) {
	for _, flag := range s.order {
		if flag.changed {
			fn(flag)
		}
	}
}

func (s *flagSet) add(flag *flag) {
	flag.Name = flag.name
	flag.Value = flagValue{flag: flag}
	s.byName[flag.name] = flag
	if flag.short != "" {
		s.byShort[flag.short] = flag
	}
	s.order = append(s.order, flag)
}

func (s *flagSet) lookupLong(name string) *flag {
	if s == nil {
		return nil
	}
	return s.byName[name]
}

func (s *flagSet) lookupShort(name string) *flag {
	if s == nil {
		return nil
	}
	return s.byShort[name]
}

func (s *flagSet) parse(args []string) ([]string, error) {
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remaining = append(remaining, args[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			remaining = append(remaining, arg)
			continue
		}
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			name, value, hasValue := strings.Cut(arg[2:], "=")
			flag := s.lookupLong(name)
			if flag == nil {
				return nil, fmt.Errorf("unknown flag: --%s", name)
			}
			if !hasValue && flag.kind != flagKindBool {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag needs a value: --%s", name)
				}
				i++
				value = args[i]
			}
			if err := flag.set(value, hasValue); err != nil {
				return nil, fmt.Errorf("invalid value for --%s: %w", name, err)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			short := arg[1:]
			flag := s.lookupShort(short)
			attachedValue := ""
			attachedHasValue := false
			if flag == nil && len(short) > 1 {
				if attachedFlag := s.lookupShort(short[:1]); attachedFlag != nil && attachedFlag.kind != flagKindBool {
					flag = attachedFlag
					attachedValue = short[1:]
					attachedHasValue = true
				}
			}
			if flag == nil {
				return nil, fmt.Errorf("unknown flag: -%s", short)
			}
			value := attachedValue
			hasValue := attachedHasValue
			if flag.kind != flagKindBool {
				if !hasValue && i+1 >= len(args) {
					return nil, fmt.Errorf("flag needs a value: -%s", short)
				}
				if !hasValue {
					i++
					value = args[i]
					hasValue = true
				}
			}
			if err := flag.set(value, hasValue); err != nil {
				return nil, fmt.Errorf("invalid value for -%s: %w", short, err)
			}
			continue
		}
		remaining = append(remaining, arg)
	}
	return remaining, nil
}

type flagKind string

const (
	flagKindString flagKind = "string"
	flagKindInt    flagKind = "int"
	flagKindBool   flagKind = "bool"
)

type flag struct {
	Name  string
	Value flagValue

	name        string
	short       string
	usage       string
	kind        flagKind
	changed     bool
	stringValue *string
	intValue    *int
	boolValue   *bool
}

func (f *flag) set(value string, hasValue bool) error {
	switch f.kind {
	case flagKindString:
		*f.stringValue = value
	case flagKindInt:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*f.intValue = parsed
	case flagKindBool:
		if !hasValue {
			*f.boolValue = true
			f.changed = true
			return nil
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		*f.boolValue = parsed
	}
	f.changed = true
	return nil
}

type flagValue struct {
	flag *flag
}

func (v flagValue) String() string {
	switch v.flag.kind {
	case flagKindString:
		return *v.flag.stringValue
	case flagKindInt:
		return strconv.Itoa(*v.flag.intValue)
	case flagKindBool:
		return strconv.FormatBool(*v.flag.boolValue)
	default:
		return ""
	}
}

type color string

type style struct {
	bold       bool
	foreground color
}

func newStyle() style {
	return style{}
}

func (s style) Bold(value bool) style {
	s.bold = value
	return s
}

func (s style) Foreground(value color) style {
	s.foreground = value
	return s
}

func (s style) Render(text string) string {
	if !shouldRenderColor() {
		return text
	}

	var codes []string
	if s.bold {
		codes = append(codes, "1")
	}
	if s.foreground != "" {
		code, err := strconv.Atoi(string(s.foreground))
		if err == nil && code >= 0 && code <= 255 {
			codes = append(codes, fmt.Sprintf("38;5;%d", code))
		}
	}
	if len(codes) == 0 {
		return text
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + text + "\x1b[0m"
}

func shouldRenderColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
