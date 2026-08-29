package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

const (
	pipCommandName    = "pip"
	pip3CommandName   = "pip3"
	uvCommandName     = "uv"
	poetryCommandName = "poetry"
	pythonListFormat  = "--format=json"
)

var pythonPackageValueFlags = map[string]bool{
	"-r":                true,
	"--requirement":     true,
	"-c":                true,
	"--constraint":      true,
	"-i":                true,
	"--index-url":       true,
	"--extra-index-url": true,
	"-f":                true,
	"--find-links":      true,
	"--trusted-host":    true,
	"--python":          true,
	"--python-version":  true,
	"--platform":        true,
	"--target":          true,
	"--prefix":          true,
	"--root":            true,
	"--group":           true,
	"--with":            true,
	"--without":         true,
	"--from":            true,
	"-E":                true,
	"--extras":          true,
}

type PipMonitor struct {
	*ProcessMonitor
	commandName string
}

func NewPipMonitor() Monitor {
	return &PipMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolPip, pipCommandName),
		commandName:    pipCommandName,
	}
}

func (m *PipMonitor) Initialize(config *core.Config) error {
	if commandName, err := firstAvailableCommand(pip3CommandName, pipCommandName); err == nil {
		m.commandName = commandName
		m.binaryPath = commandName
	} else {
		return fmt.Errorf("pip not found: %w", err)
	}

	return m.ProcessMonitor.Initialize(config)
}

func (m *PipMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newPythonExecutionRecord(core.ToolPip, cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	applyPipSubcommand(record, args[0], args[1:])
	return record, nil
}

func newPythonExecutionRecord(tool, cmd string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:     tool,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
}

func applyPipSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	record.Metadata["subcommand"] = subcommand
	switch subcommand {
	case "install":
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = "install"
	case "uninstall", "remove":
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = "uninstall"
	case "list":
		record.Metadata["action"] = "list"
	case "freeze":
		record.Metadata["action"] = "freeze"
	case "show":
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = "show"
	}
}

//nolint:legibility // Monitor interface requires this method name.
func (m *PipMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	jsonOutput := true
	output, err := runPipListCommandOutput(m.commandName, jsonOutput)
	if err == nil && len(output) > 0 {
		if packages, parseErr := parsePythonPackageJSON(core.ToolPip, output); parseErr == nil {
			return packages, nil
		}
	}

	jsonOutput = false
	output, err = runPipListCommandOutput(m.commandName, jsonOutput)
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("failed to list pip packages: %w", err)
	}
	return parsePythonPackageLines(core.ToolPip, string(output)), nil
}

func (m *PipMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}

type UVMonitor struct {
	*ProcessMonitor
}

func NewUVMonitor() Monitor {
	return &UVMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolUV, uvCommandName),
	}
}

func (m *UVMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(uvCommandName); err != nil {
		return fmt.Errorf("uv not found: %w", err)
	}
	return m.ProcessMonitor.Initialize(config)
}

func (m *UVMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newPythonExecutionRecord(core.ToolUV, cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	applyUVSubcommand(record, args[0], args[1:])
	return record, nil
}

func applyUVSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	record.Metadata["subcommand"] = subcommand
	switch subcommand {
	case "pip":
		parseUVPipCommand(record, args)
	case "tool":
		parseUVToolCommand(record, args)
	case "add":
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = "add"
	case "remove":
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = "remove"
	case "sync", "lock", "run":
		record.Metadata["action"] = subcommand
	}
}

//nolint:legibility // Monitor interface requires this method name.
func (m *UVMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	output, err := exec.Command(uvCommandName, "tool", "list").Output()
	if err == nil && len(output) > 0 {
		return parseUVToolList(string(output)), nil
	}

	output, err = exec.Command(uvCommandName, "pip", "list", pythonListFormat).Output()
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("failed to list uv packages: %w", err)
	}
	if packages, parseErr := parsePythonPackageJSON(core.ToolUV, output); parseErr == nil {
		return packages, nil
	}
	return parsePythonPackageLines(core.ToolUV, string(output)), nil
}

func (m *UVMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}

type PoetryMonitor struct {
	*ProcessMonitor
}

func NewPoetryMonitor() Monitor {
	return &PoetryMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolPoetry, poetryCommandName),
	}
}

func (m *PoetryMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(poetryCommandName); err != nil {
		return fmt.Errorf("poetry not found: %w", err)
	}
	return m.ProcessMonitor.Initialize(config)
}

func (m *PoetryMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newPythonExecutionRecord(core.ToolPoetry, cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	applyPoetrySubcommand(record, args[0], args[1:])
	return record, nil
}

func applyPoetrySubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	record.Metadata["subcommand"] = subcommand

	if isPoetryPackageAction(subcommand) {
		record.PackagesAffected = extractPythonPackages(args)
		record.Metadata["action"] = subcommand
		return
	}
	if isPoetryLifecycleAction(subcommand) {
		record.Metadata["action"] = subcommand
		return
	}
	if subcommand == "self" {
		parsePoetrySelfCommand(record, args)
	}
}

func isPoetryPackageAction(subcommand string) bool {
	switch subcommand {
	case "add", "remove", "update", "show":
		return true
	default:
		return false
	}
}

func isPoetryLifecycleAction(subcommand string) bool {
	switch subcommand {
	case "install", "sync", "lock":
		return true
	default:
		return false
	}
}

//nolint:legibility // Monitor interface requires this method name.
func (m *PoetryMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, nil
}

func (m *PoetryMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}

func firstAvailableCommand(names ...string) (string, error) {
	var lastErr error
	for _, name := range names {
		_, err := exec.LookPath(name)
		if err == nil {
			return name, nil
		}
		lastErr = err
	}
	return "", lastErr
}

var pipListCommandOutputs = map[string]func(bool) ([]byte, error){
	pip3CommandName: runPip3ListCommandOutput,
	pipCommandName:  runPipCommandListOutput,
}

func runPipListCommandOutput(commandName string, formatted bool) ([]byte, error) {
	runCommand, ok := pipListCommandOutputs[commandName]
	if !ok {
		return nil, fmt.Errorf("unsupported pip command: %s", commandName)
	}
	return runCommand(formatted)
}

func runPip3ListCommandOutput(formatted bool) ([]byte, error) {
	if formatted {
		return exec.Command(pip3CommandName, "list", pythonListFormat).Output()
	}
	return exec.Command(pip3CommandName, "list").Output()
}

func runPipCommandListOutput(formatted bool) ([]byte, error) {
	if formatted {
		return exec.Command(pipCommandName, "list", pythonListFormat).Output()
	}
	return exec.Command(pipCommandName, "list").Output()
}

func parseUVPipCommand(record *core.ExecutionRecord, args []string) {
	if len(args) == 0 {
		return
	}
	pipCommand := args[0]
	record.Metadata["pip_command"] = pipCommand
	switch pipCommand {
	case "install":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "pip_install"
	case "uninstall":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "pip_uninstall"
	case "list":
		record.Metadata["action"] = "pip_list"
	case "freeze":
		record.Metadata["action"] = "pip_freeze"
	}
}

func parseUVToolCommand(record *core.ExecutionRecord, args []string) {
	if len(args) == 0 {
		return
	}
	toolCommand := args[0]
	record.Metadata["tool_command"] = toolCommand
	switch toolCommand {
	case "install":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "tool_install"
	case "uninstall":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "tool_uninstall"
	case "run":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "tool_run"
	case "list":
		record.Metadata["action"] = "tool_list"
	}
}

func parsePoetrySelfCommand(record *core.ExecutionRecord, args []string) {
	if len(args) == 0 {
		return
	}
	selfCommand := args[0]
	record.Metadata["self_command"] = selfCommand
	switch selfCommand {
	case "add":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "self_add"
	case "remove":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "self_remove"
	case "show":
		record.Metadata["action"] = "self_show"
	}
}

func extractPythonPackages(args []string) []string {
	var packages []string
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		pkg, skipsNextArg := pythonPackageFromArg(arg)
		if skipsNextArg {
			skipNext = true
			continue
		}
		if pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func pythonPackageFromArg(arg string) (string, bool) {
	if arg == "" {
		return "", false
	}
	if strings.HasPrefix(arg, "-") {
		return "", pythonPackageValueFlags[arg]
	}
	return cleanPythonPackageSpec(arg), false
}

func cleanPythonPackageSpec(spec string) string {
	spec = strings.Trim(strings.TrimSpace(spec), `"'`)
	if skipsPythonPackageSpec(spec) {
		return ""
	}
	if at := strings.Index(spec, " @ "); at > 0 {
		spec = spec[:at]
	}
	if bracket := strings.Index(spec, "["); bracket > 0 {
		spec = spec[:bracket]
	}
	if op := strings.IndexAny(spec, "=<>!~"); op > 0 {
		spec = spec[:op]
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	return spec
}

func skipsPythonPackageSpec(spec string) bool {
	isEmpty := spec == ""
	isCurrentDir := spec == "."
	isRelativePath := strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
	isAbsolutePath := strings.HasPrefix(spec, "/")
	isURL := strings.Contains(spec, "://")
	skipSpec := isEmpty || isCurrentDir || isRelativePath || isAbsolutePath || isURL
	return skipSpec
}

type pythonPackageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func parsePythonPackageJSON(tool string, output []byte) ([]*core.PackageInfo, error) {
	var raw []pythonPackageJSON
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, err
	}
	packages := make([]*core.PackageInfo, 0, len(raw))
	for _, item := range raw {
		if item.Name == "" {
			continue
		}
		packages = append(packages, &core.PackageInfo{
			Name:        item.Name,
			Version:     item.Version,
			Tool:        tool,
			InstallDate: time.Now(),
		})
	}
	return packages, nil
}

func parsePythonPackageLines(tool, output string) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		pkg := pythonPackageFromLine(tool, scanner.Text())
		if pkg == nil {
			continue
		}
		packages = append(packages, pkg)
	}
	return packages
}

func pythonPackageFromLine(tool, line string) *core.PackageInfo {
	line = strings.TrimSpace(line)
	if skipPythonPackageLine(line) {
		return nil
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	return &core.PackageInfo{
		Name:        fields[0],
		Version:     fields[1],
		Tool:        tool,
		InstallDate: time.Now(),
	}
}

func skipPythonPackageLine(line string) bool {
	if line == "" {
		return true
	}
	if strings.HasPrefix(line, "Package ") {
		return true
	}
	return strings.HasPrefix(line, "---")
}

func parseUVToolList(output string) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		pkg := parseUVToolPackage(scanner.Text())
		if pkg == nil {
			continue
		}
		packages = append(packages, pkg)
	}
	return packages
}

func parseUVToolPackage(text string) *core.PackageInfo {
	line := strings.TrimSpace(text)
	if skipUVToolLine(line) {
		return nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	return &core.PackageInfo{
		Name:        fields[0],
		Version:     uvToolVersion(fields),
		Tool:        core.ToolUV,
		InstallDate: time.Now(),
	}
}

func uvToolVersion(fields []string) string {
	hasVersion := len(fields) > 1 && looksLikeVersion(fields[1])
	if hasVersion {
		return fields[1]
	}
	return ""
}

func skipUVToolLine(line string) bool {
	if line == "" {
		return true
	}
	return strings.HasPrefix(line, "-")
}
