package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
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

//nolint:legibility // Monitor interface requires this method name.
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
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
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

//nolint:legibility // Monitor interface requires this method name.
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
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}

func parseJavaScriptManagerCommand(tool, cmd string, args []string) *core.ExecutionRecord {
	record := newJavaScriptExecutionRecord(tool, cmd, args)
	if len(args) == 0 {
		return record
	}
	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	record.Metadata["global"] = contains(args, jsGlobalShortFlag) || contains(args, jsGlobalLongFlag)
	applyJavaScriptSubcommand(record, subcommand, args)
	return record
}

func newJavaScriptExecutionRecord(tool, cmd string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:     tool,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
}

func applyJavaScriptSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "install", "i", "add":
		applyJavaScriptInstall(record, args)
	case "uninstall", "remove", "rm", "r", "un":
		applyJavaScriptUninstall(record, args)
	case "update", "up", "upgrade":
		applyJavaScriptUpdate(record, args)
	case "list", "ls", "pm":
		record.Metadata["action"] = "list"
	case "run", "run-script":
		applyJavaScriptRun(record, args)
	case "dlx", "x", "exec":
		record.Metadata["action"] = "exec"
		setJavaScriptExecPackage(record, args)
	}
}

func applyJavaScriptInstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = extractJavaScriptPackages(args[1:])
	record.Metadata["action"] = "install"
}

func applyJavaScriptUninstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = extractJavaScriptPackages(args[1:])
	record.Metadata["action"] = "uninstall"
}

func applyJavaScriptUpdate(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = extractJavaScriptPackages(args[1:])
	if len(record.PackagesAffected) == 0 {
		record.Metadata["update_all"] = true
	}
}

func applyJavaScriptRun(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "run"
	if len(args) > 1 {
		record.Metadata["script"] = args[1]
	}
}

func setJavaScriptExecPackage(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
	if strings.HasPrefix(args[1], "-") {
		return
	}
	if pkg := cleanJavaScriptPackageSpec(args[1]); pkg != "" {
		record.PackagesAffected = []string{pkg}
	}
}

func extractJavaScriptPackages(args []string) []string {
	valueFlags := javaScriptValueFlags()
	var packages []string
	skipNext := false
	for _, arg := range args {
		if skipJavaScriptPackageArg(arg, valueFlags, &skipNext) {
			continue
		}
		if pkg := cleanJavaScriptPackageSpec(arg); pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func javaScriptValueFlags() map[string]bool {
	return map[string]bool{
		"--registry": true,
		"--scope":    true,
		"--tag":      true,
		"--dir":      true,
		"--filter":   true,
		"-C":         true,
	}
}

func skipJavaScriptPackageArg(arg string, valueFlags map[string]bool, skipNext *bool) bool {
	if *skipNext {
		*skipNext = false
		return true
	}
	if arg == "" {
		return true
	}
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	if valueFlags[arg] {
		*skipNext = true
	}
	return true
}

func cleanJavaScriptPackageSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if invalidJavaScriptPackageSpec(spec) {
		return ""
	}
	if strings.HasPrefix(spec, "@") {
		return cleanScopedJavaScriptPackageSpec(spec)
	}
	if at := strings.Index(spec, "@"); at > 0 {
		return spec[:at]
	}
	return spec
}

func invalidJavaScriptPackageSpec(spec string) bool {
	if spec == "" {
		return true
	}
	if strings.HasPrefix(spec, ".") {
		return true
	}
	return strings.Contains(spec, "://")
}

func cleanScopedJavaScriptPackageSpec(spec string) string {
	segments := strings.Split(spec, "/")
	if len(segments) == 0 {
		return ""
	}
	if len(segments) <= 1 {
		return spec
	}
	name := segments[0] + "/" + segments[1]
	if at := strings.LastIndex(name, "@"); at > 0 {
		return name[:at]
	}
	return name
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
		mergeNodeDeps(seen, project.Dependencies)
		mergeNodeDeps(seen, project.DevDependencies)
		mergeNodeDeps(seen, project.OptionalDependencies)
	}
	return packagesFromNodeDeps(tool, seen)
}

func mergeNodeDeps(seen map[string]nodePackageInfo, deps map[string]nodePackageInfo) {
	for name, info := range deps {
		seen[name] = info
	}
}

func packagesFromNodeDeps(tool string, deps map[string]nodePackageInfo) []*core.PackageInfo {
	names := sortedNodePackageNames(deps)
	packages := make([]*core.PackageInfo, len(names))
	for index, name := range names {
		info := deps[name]
		packages[index] = &core.PackageInfo{
			Name:        name,
			Version:     info.Version,
			Tool:        tool,
			InstallDate: time.Now(),
			Path:        info.Path,
		}
	}
	return packages
}

func sortedNodePackageNames(deps map[string]nodePackageInfo) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func parseSimplePackageLines(tool, output string) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		pkg := simplePackageFromLine(tool, scanner.Text())
		if pkg == nil {
			continue
		}
		packages = append(packages, pkg)
	}
	return packages
}

func simplePackageFromLine(tool, line string) *core.PackageInfo {
	name, version, ok := simplePackageParts(line)
	if !ok {
		return nil
	}
	return &core.PackageInfo{
		Name:        name,
		Version:     version,
		Tool:        tool,
		InstallDate: time.Now(),
	}
}

func simplePackageParts(line string) (string, string, bool) {
	line = normalizeSimplePackageLine(line)
	if skipSimplePackageLine(line) {
		return "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}
	name, version := splitPackageVersion(fields[0])
	if name == "" {
		return "", "", false
	}
	version = packageVersionFromFields(version, fields)
	return name, version, true
}

func normalizeSimplePackageLine(line string) string {
	line = strings.TrimSpace(line)
	line = trimTreeLinePrefix(line)
	return strings.TrimPrefix(line, "- ")
}

func trimTreeLinePrefix(line string) string {
	line = strings.TrimPrefix(line, "├── ")
	line = strings.TrimPrefix(line, "└── ")
	line = strings.TrimPrefix(line, "├─┬ ")
	return strings.TrimPrefix(line, "└─┬ ")
}

func skipSimplePackageLine(line string) bool {
	if line == "" {
		return true
	}
	if strings.HasSuffix(line, ":") {
		return true
	}
	return strings.HasPrefix(line, "/")
}

func packageVersionFromFields(version string, fields []string) string {
	if version != "" {
		return version
	}
	if len(fields) <= 1 {
		return ""
	}
	if !looksLikeVersion(fields[1]) {
		return ""
	}
	return fields[1]
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
	hasNumericPrefix := value[0] >= '0' && value[0] <= '9'
	return hasNumericPrefix
}
