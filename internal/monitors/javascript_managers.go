package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

const (
	pnpmCommandName = "pnpm"
	bunCommandName  = "bun"

	jsGlobalShortFlag = "-g"
	jsGlobalLongFlag  = "--global"
)

type PNPMMonitor struct {
	*ProcessMonitor
}

func NewPNPMMonitor() Monitor {
	return &PNPMMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolPNPM, pnpmCommandName),
	}
}

func (m *PNPMMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(pnpmCommandName); err != nil {
		return fmt.Errorf("pnpm not found: %w", err)
	}
	return m.ProcessMonitor.Initialize(config)
}

func (m *PNPMMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return parseJavaScriptManagerCommand(core.ToolPNPM, cmd, args), nil
}

func (m *PNPMMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	output, err := exec.Command(pnpmCommandName, "list", jsGlobalShortFlag, "--depth=0", "--json").Output()
	if err == nil && len(output) > 0 {
		if packages, parseErr := parseNodePackageJSON(core.ToolPNPM, output); parseErr == nil {
			return packages, nil
		}
	}

	output, err = exec.Command(pnpmCommandName, "list", jsGlobalShortFlag, "--depth=0").Output()
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("failed to list global pnpm packages: %w", err)
	}
	return parseSimplePackageLines(core.ToolPNPM, string(output)), nil
}

func (m *PNPMMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}

type BunMonitor struct {
	*ProcessMonitor
}

func NewBunMonitor() Monitor {
	return &BunMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolBun, bunCommandName),
	}
}

func (m *BunMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(bunCommandName); err != nil {
		return fmt.Errorf("bun not found: %w", err)
	}
	return m.ProcessMonitor.Initialize(config)
}

func (m *BunMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return parseJavaScriptManagerCommand(core.ToolBun, cmd, args), nil
}

func (m *BunMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	output, err := exec.Command(bunCommandName, "pm", "ls", jsGlobalShortFlag, "--json").Output()
	if err == nil && len(output) > 0 {
		if packages, parseErr := parseNodePackageJSON(core.ToolBun, output); parseErr == nil {
			return packages, nil
		}
	}

	output, err = exec.Command(bunCommandName, "pm", "ls", jsGlobalShortFlag).Output()
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("failed to list global bun packages: %w", err)
	}
	return parseSimplePackageLines(core.ToolBun, string(output)), nil
}

func (m *BunMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}

func parseJavaScriptManagerCommand(tool, cmd string, args []string) *core.ExecutionRecord {
	record := &core.ExecutionRecord{
		Tool:     tool,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}

	if len(args) == 0 {
		return record
	}

	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	record.Metadata["global"] = contains(args, jsGlobalShortFlag) || contains(args, jsGlobalLongFlag)

	switch subcommand {
	case "install", "i", "add":
		record.PackagesAffected = extractJavaScriptPackages(args[1:])
		record.Metadata["action"] = "install"
	case "uninstall", "remove", "rm", "r", "un":
		record.PackagesAffected = extractJavaScriptPackages(args[1:])
		record.Metadata["action"] = "uninstall"
	case "update", "up", "upgrade":
		record.PackagesAffected = extractJavaScriptPackages(args[1:])
		if len(record.PackagesAffected) == 0 {
			record.Metadata["update_all"] = true
		}
	case "list", "ls", "pm":
		record.Metadata["action"] = "list"
	case "run", "run-script":
		record.Metadata["action"] = "run"
		if len(args) > 1 {
			record.Metadata["script"] = args[1]
		}
	case "dlx", "x", "exec":
		record.Metadata["action"] = "exec"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			if pkg := cleanJavaScriptPackageSpec(args[1]); pkg != "" {
				record.PackagesAffected = []string{pkg}
			}
		}
	}

	return record
}

func extractJavaScriptPackages(args []string) []string {
	valueFlags := map[string]bool{
		"--registry": true,
		"--scope":    true,
		"--tag":      true,
		"--dir":      true,
		"--filter":   true,
		"-C":         true,
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
		if pkg := cleanJavaScriptPackageSpec(arg); pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func cleanJavaScriptPackageSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, ".") || strings.Contains(spec, "://") {
		return ""
	}
	if strings.HasPrefix(spec, "@") {
		segments := strings.Split(spec, "/")
		if len(segments) == 0 {
			return ""
		}
		if len(segments) > 1 {
			name := segments[0] + "/" + segments[1]
			if at := strings.LastIndex(name, "@"); at > 0 {
				return name[:at]
			}
			return name
		}
		return spec
	}
	if at := strings.Index(spec, "@"); at > 0 {
		return spec[:at]
	}
	return spec
}

type nodePackageInfo struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

type nodePackageList struct {
	Dependencies         map[string]nodePackageInfo `json:"dependencies"`
	DevDependencies      map[string]nodePackageInfo `json:"devDependencies"`
	OptionalDependencies map[string]nodePackageInfo `json:"optionalDependencies"`
}

func parseNodePackageJSON(tool string, output []byte) ([]*core.PackageInfo, error) {
	var projects []nodePackageList
	if err := json.Unmarshal(output, &projects); err == nil {
		return packagesFromNodeLists(tool, projects), nil
	}

	var project nodePackageList
	if err := json.Unmarshal(output, &project); err == nil {
		return packagesFromNodeLists(tool, []nodePackageList{project}), nil
	}

	var direct map[string]nodePackageInfo
	if err := json.Unmarshal(output, &direct); err == nil {
		return packagesFromNodeDeps(tool, direct), nil
	}

	return nil, fmt.Errorf("unsupported package JSON")
}

func packagesFromNodeLists(tool string, projects []nodePackageList) []*core.PackageInfo {
	seen := make(map[string]nodePackageInfo)
	for _, project := range projects {
		for name, info := range project.Dependencies {
			seen[name] = info
		}
		for name, info := range project.DevDependencies {
			seen[name] = info
		}
		for name, info := range project.OptionalDependencies {
			seen[name] = info
		}
	}
	return packagesFromNodeDeps(tool, seen)
}

func packagesFromNodeDeps(tool string, deps map[string]nodePackageInfo) []*core.PackageInfo {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	packages := make([]*core.PackageInfo, 0, len(names))
	for _, name := range names {
		info := deps[name]
		packages = append(packages, &core.PackageInfo{
			Name:        name,
			Version:     info.Version,
			Tool:        tool,
			InstallDate: time.Now(),
			Path:        info.Path,
		})
	}
	return packages
}

func parseSimplePackageLines(tool, output string) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "├── ")
		line = strings.TrimPrefix(line, "└── ")
		line = strings.TrimPrefix(line, "├─┬ ")
		line = strings.TrimPrefix(line, "└─┬ ")
		line = strings.TrimPrefix(line, "- ")
		if line == "" || strings.HasSuffix(line, ":") || strings.HasPrefix(line, "/") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name, version := splitPackageVersion(fields[0])
		if name == "" {
			continue
		}
		if version == "" && len(fields) > 1 && looksLikeVersion(fields[1]) {
			version = fields[1]
		}
		packages = append(packages, &core.PackageInfo{
			Name:        name,
			Version:     version,
			Tool:        tool,
			InstallDate: time.Now(),
		})
	}
	return packages
}

func splitPackageVersion(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if strings.HasPrefix(value, "@") {
		at := strings.LastIndex(value, "@")
		if at > 0 {
			return value[:at], value[at+1:]
		}
		return value, ""
	}
	if at := strings.LastIndex(value, "@"); at > 0 {
		return value[:at], value[at+1:]
	}
	return value, ""
}

func looksLikeVersion(value string) bool {
	value = strings.TrimPrefix(value, "v")
	if value == "" {
		return false
	}
	return value[0] >= '0' && value[0] <= '9'
}
