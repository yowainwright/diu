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
	record := &core.ExecutionRecord{
		Tool:     core.ToolPip,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
	if len(args) == 0 {
		return record, nil
	}

	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	switch subcommand {
	case "install":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "install"
	case "uninstall", "remove":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "uninstall"
	case "list":
		record.Metadata["action"] = "list"
	case "freeze":
		record.Metadata["action"] = "freeze"
	case "show":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "show"
	}
	return record, nil
}

func (m *PipMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	output, err := exec.Command(m.commandName, "list", pythonListFormat).Output()
	if err == nil && len(output) > 0 {
		if packages, parseErr := parsePythonPackageJSON(core.ToolPip, output); parseErr == nil {
			return packages, nil
		}
	}

	output, err = exec.Command(m.commandName, "list").Output()
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("failed to list pip packages: %w", err)
	}
	return parsePythonPackageLines(core.ToolPip, string(output)), nil
}

func (m *PipMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
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
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}
	if _, err := exec.LookPath(uvCommandName); err != nil {
		return fmt.Errorf("uv not found: %w", err)
	}
	return nil
}

func (m *UVMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := &core.ExecutionRecord{
		Tool:     core.ToolUV,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
	if len(args) == 0 {
		return record, nil
	}

	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	switch subcommand {
	case "pip":
		parseUVPipCommand(record, args[1:])
	case "tool":
		parseUVToolCommand(record, args[1:])
	case "add":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "add"
	case "remove":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "remove"
	case "sync", "lock", "run":
		record.Metadata["action"] = subcommand
	}
	return record, nil
}

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
	return m.ProcessMonitor.Start(ctx, eventChan)
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
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}
	if _, err := exec.LookPath(poetryCommandName); err != nil {
		return fmt.Errorf("poetry not found: %w", err)
	}
	return nil
}

func (m *PoetryMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := &core.ExecutionRecord{
		Tool:     core.ToolPoetry,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
	if len(args) == 0 {
		return record, nil
	}

	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	switch subcommand {
	case "add":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "add"
	case "remove":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "remove"
	case "update":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "update"
	case "show":
		record.PackagesAffected = extractPythonPackages(args[1:])
		record.Metadata["action"] = "show"
	case "install", "sync", "lock":
		record.Metadata["action"] = subcommand
	case "self":
		parsePoetrySelfCommand(record, args[1:])
	}
	return record, nil
}

func (m *PoetryMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, nil
}

func (m *PoetryMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}

func firstAvailableCommand(names ...string) (string, error) {
	var lastErr error
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		} else {
			lastErr = err
		}
	}
	return "", lastErr
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
	valueFlags := map[string]bool{
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
		"-E":                true,
		"--extras":          true,
	}

	var packages []string
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if valueFlags[arg] {
				skipNext = true
			}
			continue
		}
		if pkg := cleanPythonPackageSpec(arg); pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func cleanPythonPackageSpec(spec string) string {
	spec = strings.Trim(strings.TrimSpace(spec), `"'`)
	if spec == "" || spec == "." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") || strings.Contains(spec, "://") {
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
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Package ") || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		packages = append(packages, &core.PackageInfo{
			Name:        fields[0],
			Version:     fields[1],
			Tool:        tool,
			InstallDate: time.Now(),
		})
	}
	return packages
}

func parseUVToolList(output string) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		version := ""
		if len(fields) > 1 && looksLikeVersion(fields[1]) {
			version = fields[1]
		}
		packages = append(packages, &core.PackageInfo{
			Name:        fields[0],
			Version:     version,
			Tool:        core.ToolUV,
			InstallDate: time.Now(),
		})
	}
	return packages
}
