package dx

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Command struct {
	Use         string
	Short       string
	Long        string
	Hidden      bool
	RunE        func(*Command, []string) error
	Version     func() string
	Output      io.Writer
	ErrorOutput io.Writer

	parent   *Command
	flags    *FlagSet
	commands []*Command
}

func (c *Command) AddCommand(commands ...*Command) {
	for _, child := range commands {
		child.parent = c
		c.commands = append(c.commands, child)
	}
}

//nolint:legibility // Public command API mirrors Cobra's Execute method.
func (c *Command) Execute(args []string) error {
	return c.execute(args)
}

func (c *Command) execute(args []string) error {
	handled, err := c.handleBuiltIn(args)
	shouldReturn := handled || err != nil
	if shouldReturn {
		return err
	}
	if child := c.child(args); child != nil {
		return child.execute(args[1:])
	}
	return c.run(args)
}

func (c *Command) handleBuiltIn(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "help":
		return true, c.printHelp(args[1:])
	case "-h", "--help":
		c.printUsage(c.stdout())
		return true, nil
	case "-v", "--version":
		return c.printVersion()
	default:
		return false, nil
	}
}

func (c *Command) printVersion() (bool, error) {
	root := c.root()
	isSubcommand := c.parent != nil
	missingVersion := root.Version == nil
	skipVersion := isSubcommand || missingVersion
	if skipVersion {
		return false, nil
	}
	_, err := fmt.Fprintln(c.stdout(), root.Version())
	return true, err
}

func (c *Command) child(args []string) *Command {
	if len(args) == 0 {
		return nil
	}
	return c.findCommand(args[0])
}

func (c *Command) run(args []string) error {
	remaining, err := c.Flags().Parse(args)
	if err != nil {
		return err
	}
	if hasHelpFlag(remaining) {
		c.printUsage(c.stdout())
		return nil
	}
	return c.runHandler(remaining)
}

func (c *Command) runHandler(args []string) error {
	if c.RunE != nil {
		return c.RunE(c, args)
	}
	if len(args) > 0 {
		c.printUsage(c.stderr())
		return fmt.Errorf("unknown command: %s", args[0])
	}
	c.printUsage(c.stdout())
	return nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		isHelpFlag := arg == "-h" || arg == "--help"
		if isHelpFlag {
			return true
		}
	}
	return false
}

func (c *Command) Flags() *FlagSet {
	if c.flags == nil {
		c.flags = NewFlagSet()
	}
	return c.flags
}

func (c *Command) PrintUsage() {
	c.printUsage(c.stdout())
}

func (c *Command) PrintErrorUsage() {
	c.printUsage(c.stderr())
}

//nolint:legibility // Public command API mirrors Cobra's Flag method.
func (c *Command) Flag(name string) *Flag {
	return c.Flags().lookupLong(name)
}

func (c *Command) findCommand(name string) *Command {
	for _, child := range c.commands {
		if CommandName(child.Use) == name {
			return child
		}
	}
	return nil
}

func (c *Command) printHelp(args []string) error {
	if len(args) == 0 {
		c.printUsage(c.stdout())
		return nil
	}
	child := c.findCommand(args[0])
	if child == nil {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	return child.printHelp(args[1:])
}

func (c *Command) printUsage(w io.Writer) {
	c.printDescription(w)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Usage: %s\n", c.usagePath())
	c.printCommands(w)
	c.printFlags(w)
}

func (c *Command) printDescription(w io.Writer) {
	if c.Long != "" {
		_, _ = fmt.Fprintln(w, c.Long)
		return
	}
	if c.Short != "" {
		_, _ = fmt.Fprintln(w, c.Short)
	}
}

func (c *Command) printCommands(w io.Writer) {
	if len(c.commands) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nCommands:")
	for _, child := range c.commands {
		if !child.Hidden {
			_, _ = fmt.Fprintf(w, "  %-14s %s\n", CommandName(child.Use), child.Short)
		}
	}
}

func (c *Command) printFlags(w io.Writer) {
	hasFlags := c.flags != nil
	hasOrderedFlags := hasFlags && len(c.flags.order) > 0
	if !hasOrderedFlags {
		return
	}
	_, _ = fmt.Fprintln(w, "\nFlags:")
	for _, current := range c.flags.order {
		_, _ = fmt.Fprintf(w, "  %s--%-16s %s\n", current.shortUsage(), current.name, current.usage)
	}
}

func (c *Command) usagePath() string {
	var parts []string
	for current := c; current != nil; current = current.parent {
		parts = append([]string{current.Use}, parts...)
	}
	return strings.Join(parts, " ")
}

func (c *Command) root() *Command {
	current := c
	for current.parent != nil {
		current = current.parent
	}
	return current
}

func (c *Command) stdout() io.Writer {
	if output := c.root().Output; output != nil {
		return output
	}
	return os.Stdout
}

func (c *Command) stderr() io.Writer {
	if output := c.root().ErrorOutput; output != nil {
		return output
	}
	return os.Stderr
}

func CommandName(use string) string {
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

type FlagSet struct {
	byName  map[string]*Flag
	byShort map[string]*Flag
	order   []*Flag
}

func NewFlagSet() *FlagSet {
	return &FlagSet{
		byName:  make(map[string]*Flag),
		byShort: make(map[string]*Flag),
	}
}

func (s *FlagSet) StringVarP(target *string, name, shorthand, value, usage string) {
	*target = value
	s.add(&Flag{name: name, short: shorthand, usage: usage, kind: flagKindString, stringValue: target})
}

func (s *FlagSet) StringVar(target *string, name, value, usage string) {
	s.StringVarP(target, name, "", value, usage)
}

func (s *FlagSet) IntVarP(target *int, name, shorthand string, value int, usage string) {
	*target = value
	s.add(&Flag{name: name, short: shorthand, usage: usage, kind: flagKindInt, intValue: target})
}

func (s *FlagSet) IntVar(target *int, name string, value int, usage string) {
	s.IntVarP(target, name, "", value, usage)
}

func (s *FlagSet) BoolVarP(target *bool, name, shorthand string, value bool, usage string) {
	*target = value
	s.add(&Flag{name: name, short: shorthand, usage: usage, kind: flagKindBool, boolValue: target})
}

func (s *FlagSet) BoolVar(target *bool, name string, value bool, usage string) {
	s.BoolVarP(target, name, "", value, usage)
}

func (s *FlagSet) GetString(name string) (string, error) {
	current := s.lookupLong(name)
	hasFlag := current != nil
	hasStringKind := hasFlag && current.kind == flagKindString
	if !hasStringKind {
		return "", fmt.Errorf("unknown string flag: %s", name)
	}
	return *current.stringValue, nil
}

func (s *FlagSet) GetInt(name string) (int, error) {
	current := s.lookupLong(name)
	hasFlag := current != nil
	hasIntKind := hasFlag && current.kind == flagKindInt
	if !hasIntKind {
		return 0, fmt.Errorf("unknown int flag: %s", name)
	}
	return *current.intValue, nil
}

func (s *FlagSet) GetBool(name string) (bool, error) {
	current := s.lookupLong(name)
	hasFlag := current != nil
	hasBoolKind := hasFlag && current.kind == flagKindBool
	if !hasBoolKind {
		return false, fmt.Errorf("unknown bool flag: %s", name)
	}
	return *current.boolValue, nil
}

func (s *FlagSet) Visit(fn func(*Flag)) {
	for _, current := range s.order {
		if current.changed {
			fn(current)
		}
	}
}

func (s *FlagSet) Parse(args []string) ([]string, error) {
	var remaining []string
	for index := 0; index < len(args); index++ {
		consumed, handled, rest, err := s.parseArgument(args, index)
		if err != nil {
			return nil, err
		}
		if rest != nil {
			return append(remaining, rest...), nil
		}
		if !handled {
			remaining = append(remaining, args[index])
		}
		index += consumed
	}
	return remaining, nil
}

func (s *FlagSet) parseArgument(args []string, index int) (int, bool, []string, error) {
	arg := args[index]
	if arg == "--" {
		return 0, true, append([]string{}, args[index+1:]...), nil
	}
	isHelpFlag := arg == "-h" || arg == "--help"
	if isHelpFlag {
		return 0, false, nil, nil
	}
	return s.parseFlagArgument(args, index, arg)
}

func (s *FlagSet) parseFlagArgument(args []string, index int, arg string) (int, bool, []string, error) {
	isLongFlag := strings.HasPrefix(arg, "--") && len(arg) > 2
	if isLongFlag {
		consumed, err := s.parseLong(args, index)
		return consumed, true, nil, err
	}
	isShortFlag := strings.HasPrefix(arg, "-") && len(arg) > 1
	if isShortFlag {
		consumed, err := s.parseShort(args, index)
		return consumed, true, nil, err
	}
	return 0, false, nil, nil
}

func (s *FlagSet) parseLong(args []string, index int) (int, error) {
	name, value, hasValue := strings.Cut(args[index][2:], "=")
	current := s.lookupLong(name)
	if current == nil {
		return 0, fmt.Errorf("unknown flag: --%s", name)
	}
	value, hasValue, consumed, err := flagValueFromArgs(current, value, hasValue, args, index)
	if err != nil {
		return 0, fmt.Errorf("flag needs a value: --%s", name)
	}
	if err := current.set(value, hasValue); err != nil {
		return 0, fmt.Errorf("invalid value for --%s: %w", name, err)
	}
	return consumed, nil
}

func (s *FlagSet) parseShort(args []string, index int) (int, error) {
	name := args[index][1:]
	current, value, hasValue := s.shortFlag(name)
	if current == nil {
		return 0, fmt.Errorf("unknown flag: -%s", name)
	}
	value, hasValue, consumed, err := flagValueFromArgs(current, value, hasValue, args, index)
	if err != nil {
		return 0, fmt.Errorf("flag needs a value: -%s", name)
	}
	if err := current.set(value, hasValue); err != nil {
		return 0, fmt.Errorf("invalid value for -%s: %w", name, err)
	}
	return consumed, nil
}

func (s *FlagSet) shortFlag(name string) (*Flag, string, bool) {
	if current := s.lookupShort(name); current != nil {
		return current, "", false
	}
	if len(name) < 2 {
		return nil, "", false
	}
	current := s.lookupShort(name[:1])
	if current == nil {
		return nil, "", false
	}
	if current.kind == flagKindBool {
		return nil, "", false
	}
	return current, name[1:], true
}

func flagValueFromArgs(current *Flag, value string, hasValue bool, args []string, index int) (string, bool, int, error) {
	isBoolFlag := current.kind == flagKindBool
	hasInlineValue := isBoolFlag || hasValue
	if hasInlineValue {
		return value, hasValue, 0, nil
	}
	if index+1 >= len(args) {
		return "", false, 0, io.EOF
	}
	return args[index+1], true, 1, nil
}

func (s *FlagSet) add(current *Flag) {
	current.Name = current.name
	current.Value = flagValue{flag: current}
	s.byName[current.name] = current
	if current.short != "" {
		s.byShort[current.short] = current
	}
	s.order = append(s.order, current)
}

func (s *FlagSet) lookupLong(name string) *Flag {
	if s == nil {
		return nil
	}
	return s.byName[name]
}

func (s *FlagSet) lookupShort(name string) *Flag {
	if s == nil {
		return nil
	}
	return s.byShort[name]
}

type flagKind uint8

const (
	flagKindString flagKind = iota
	flagKindInt
	flagKindBool
)

type Flag struct {
	Name  string
	Value fmt.Stringer

	name        string
	short       string
	usage       string
	kind        flagKind
	changed     bool
	stringValue *string
	intValue    *int
	boolValue   *bool
}

func (f *Flag) set(value string, hasValue bool) error {
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
		return f.setBool(value, hasValue)
	}
	f.changed = true
	return nil
}

func (f *Flag) setBool(value string, hasValue bool) error {
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
	f.changed = true
	return nil
}

func (f *Flag) shortUsage() string {
	if f.short == "" {
		return ""
	}
	usage := "-" + f.short + ", "
	return usage
}

type flagValue struct {
	flag *Flag
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
