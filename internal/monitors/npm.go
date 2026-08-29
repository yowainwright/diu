package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type NPMMonitor struct {
	*ProcessMonitor
	globalPath string
}

const (
	npmCommandName       = "npm"
	npmConfigCommand     = "config"
	npmGetCommand        = "get"
	npmPrefixConfigName  = "prefix"
	npmListCommand       = "list"
	npmGlobalFlag        = "-g"
	npmDepthZeroFlag     = "--depth=0"
	npmJSONFlag          = "--json"
	npmNodeModulesMarker = "node_modules"
)

func NewNPMMonitor() Monitor {
	return &NPMMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolNPM, "npm"),
	}
}

func (m *NPMMonitor) Initialize(config *core.Config) error {
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}

	// Find npm binary
	if _, err := exec.LookPath(npmCommandName); err != nil {
		return fmt.Errorf("npm not found: %w", err)
	}

	// Get global packages path
	m.globalPath = m.globalPathValue()

	return nil
}

func (m *NPMMonitor) globalPathValue() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, npmCommandName, npmConfigCommand, npmGetCommand, npmPrefixConfigName)
	output, err := cmd.Output()
	if err != nil {
		homeDir := os.Getenv("HOME")
		if dir, userErr := os.UserHomeDir(); userErr == nil {
			homeDir = dir
		}
		return filepath.Join(homeDir, ".npm")
	}

	prefix := strings.TrimSpace(string(output))
	return filepath.Join(prefix, "lib", "node_modules")
}

func (m *NPMMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newNPMExecutionRecord(cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	record.Metadata["global"] = hasNPMGlobalFlag(args)
	m.applyNPMSubcommand(record, subcommand, args)
	return record, nil
}

func newNPMExecutionRecord(cmd string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:     core.ToolNPM,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
}

func hasNPMGlobalFlag(args []string) bool {
	if contains(args, "-g") {
		return true
	}
	return contains(args, "--global")
}

func (m *NPMMonitor) applyNPMSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "install", "i", "add":
		m.applyNPMInstall(record, args)
	case "uninstall", "remove", "rm", "r", "un":
		m.applyNPMUninstall(record, args)
	case "update", "up", "upgrade":
		m.applyNPMUpdate(record, args)
	case "list", "ls", "la", "ll":
		m.applyNPMList(record, args)
	default:
		applyNPMMetadataSubcommand(record, subcommand, args)
	}
}

func applyNPMMetadataSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "search", "s", "se", "find":
		setNPMSearchTerm(record, args)
	case "run", "run-script":
		setNPMScript(record, args)
	case "test", "t", "tst", "start", "build", "fund", "outdated":
		setNPMDirectAction(record, subcommand)
	case "publish":
		setNPMPackageAction(record, "publish", args)
	case "link", "ln":
		setNPMPackageAction(record, "link", args)
	case "audit":
		setNPMAuditAction(record, args)
	}
}

func setNPMDirectAction(record *core.ExecutionRecord, subcommand string) {
	action := subcommand
	isTestAlias := subcommand == "t" || subcommand == "tst"
	if isTestAlias {
		action = "test"
	}
	record.Metadata["action"] = action
}

func setNPMPackageAction(record *core.ExecutionRecord, action string, args []string) {
	record.Metadata["action"] = action
	setNPMArgumentPackage(record, args)
}

func setNPMAuditAction(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "audit"
	if contains(args, "--fix") {
		record.Metadata["fix"] = true
	}
}

func (m *NPMMonitor) applyNPMInstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractPackagesFromNPMArgs(args[1:])
	record.Metadata["action"] = "install"
	if hasNPMDevDependencyFlag(args) {
		record.Metadata["dev_dependency"] = true
	}
	if hasNPMOptionalDependencyFlag(args) {
		record.Metadata["optional_dependency"] = true
	}
}

func (m *NPMMonitor) applyNPMUninstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractPackagesFromNPMArgs(args[1:])
	record.Metadata["action"] = "uninstall"
}

func (m *NPMMonitor) applyNPMUpdate(record *core.ExecutionRecord, args []string) {
	packages := m.extractPackagesFromNPMArgs(args[1:])
	if len(packages) > 0 {
		record.PackagesAffected = packages
		return
	}
	record.Metadata["update_all"] = true
}

func (m *NPMMonitor) applyNPMList(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "list"
	depth := m.extractDepth(args)
	if depth >= 0 {
		record.Metadata["depth"] = depth
	}
}

func setNPMSearchTerm(record *core.ExecutionRecord, args []string) {
	if len(args) > 1 {
		record.Metadata["search_term"] = strings.Join(args[1:], " ")
	}
}

func setNPMScript(record *core.ExecutionRecord, args []string) {
	if len(args) > 1 {
		record.Metadata["script"] = args[1]
	}
}

func hasNPMDevDependencyFlag(args []string) bool {
	if contains(args, "--save-dev") {
		return true
	}
	return contains(args, "-D")
}

func hasNPMOptionalDependencyFlag(args []string) bool {
	if contains(args, "--save-optional") {
		return true
	}
	return contains(args, "-O")
}

func setNPMArgumentPackage(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
	if strings.HasPrefix(args[1], "-") {
		return
	}
	record.PackagesAffected = []string{args[1]}
}

func (m *NPMMonitor) extractPackagesFromNPMArgs(args []string) []string {
	var packages []string
	skipNext := false

	for _, arg := range args {
		if skipNPMPackageArg(arg, &skipNext) {
			continue
		}
		name := npmPackageArgName(arg)
		if name != "" {
			packages = append(packages, name)
		}
	}

	return packages
}

func skipNPMPackageArg(arg string, skipNext *bool) bool {
	if *skipNext {
		*skipNext = false
		return true
	}
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	if npmFlagTakesValue(arg) {
		*skipNext = true
	}
	return true
}

func npmFlagTakesValue(arg string) bool {
	switch arg {
	case "--registry", "--scope", "--tag":
		return true
	default:
		return false
	}
}

func npmPackageArgName(arg string) string {
	if !strings.Contains(arg, "@") {
		return arg
	}
	if strings.HasPrefix(arg, "@") {
		parts := strings.SplitN(arg, "@", 3)
		if len(parts) >= 2 {
			return "@" + parts[1]
		}
		return ""
	}
	parts := strings.SplitN(arg, "@", 2)
	return parts[0]
}

func (m *NPMMonitor) extractDepth(args []string) int {
	for i, arg := range args {
		if arg != "--depth" {
			continue
		}
		if i+1 >= len(args) {
			continue
		}
		var depth int
		if _, err := fmt.Sscanf(args[i+1], "%d", &depth); err == nil {
			return depth
		}
	}
	return -1
}

//nolint:legibility // Monitor interface requires this method name.
func (m *NPMMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	if m.config.Tools.NPM.TrackGlobalOnly {
		return m.globalPackages()
	}
	return m.allPackages()
}

func (m *NPMMonitor) globalPackages() ([]*core.PackageInfo, error) {
	output, err := npmGlobalListOutput()
	if err != nil {
		if len(output) == 0 {
			return nil, fmt.Errorf("failed to list global packages: %w", err)
		}
	}

	dependencies, err := parseNPMGlobalList(output)
	if err != nil {
		return m.globalPackagesSimple()
	}
	return npmPackagesFromGlobalList(dependencies), nil
}

func npmGlobalListOutput() ([]byte, error) {
	cmd := exec.Command(npmCommandName, npmListCommand, npmGlobalFlag, npmDepthZeroFlag, npmJSONFlag)
	return cmd.Output()
}

type npmGlobalList struct {
	Dependencies map[string]npmGlobalDependency `json:"dependencies"`
}

type npmGlobalDependency struct {
	Version      string                 `json:"version"`
	Resolved     string                 `json:"resolved"`
	Dependencies map[string]interface{} `json:"dependencies"`
}

func parseNPMGlobalList(output []byte) (map[string]npmGlobalDependency, error) {
	var listData npmGlobalList
	err := json.Unmarshal(output, &listData)
	return listData.Dependencies, err
}

func npmPackagesFromGlobalList(dependencies map[string]npmGlobalDependency) []*core.PackageInfo {
	names := sortedNPMDependencyNames(dependencies)
	packages := make([]*core.PackageInfo, len(names))
	for index, name := range names {
		info := dependencies[name]
		packages[index] = &core.PackageInfo{
			Name:         name,
			Version:      info.Version,
			Tool:         core.ToolNPM,
			InstallDate:  time.Now(),
			Dependencies: sortedNPMDependencyKeys(info.Dependencies),
		}
	}
	return packages
}

func sortedNPMDependencyNames(dependencies map[string]npmGlobalDependency) []string {
	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func sortedNPMDependencyKeys(dependencies map[string]interface{}) []string {
	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (m *NPMMonitor) globalPackagesSimple() ([]*core.PackageInfo, error) {
	output, err := npmGlobalSimpleOutput()
	if err != nil {
		if len(output) == 0 {
			return nil, err
		}
	}
	return parseNPMGlobalSimpleOutput(output), nil
}

func npmGlobalSimpleOutput() ([]byte, error) {
	cmd := exec.Command(npmCommandName, npmListCommand, npmGlobalFlag, npmDepthZeroFlag)
	return cmd.Output()
}

func parseNPMGlobalSimpleOutput(output []byte) []*core.PackageInfo {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		pkg := npmPackageFromTreeLine(scanner.Text())
		if pkg != nil {
			packages = append(packages, pkg)
		}
	}
	return packages
}

func npmPackageFromTreeLine(line string) *core.PackageInfo {
	line, ok := npmPackageTreeEntry(line)
	if !ok {
		return nil
	}
	name, version := splitNPMNameVersion(line)
	return &core.PackageInfo{
		Name:        name,
		Version:     version,
		Tool:        core.ToolNPM,
		InstallDate: time.Now(),
	}
}

func npmPackageTreeEntry(line string) (string, bool) {
	if strings.Contains(line, npmNodeModulesMarker) {
		return "", false
	}
	if !hasNPMTreePrefix(line) {
		return "", false
	}
	line = trimTreeLinePrefix(line)
	return line, line != ""
}

func hasNPMTreePrefix(line string) bool {
	if strings.HasPrefix(line, "├") {
		return true
	}
	return strings.HasPrefix(line, "└")
}

func splitNPMNameVersion(line string) (string, string) {
	parts := strings.Split(line, "@")
	if len(parts) <= 1 {
		return line, ""
	}
	return parts[0], parts[len(parts)-1]
}

func (m *NPMMonitor) allPackages() ([]*core.PackageInfo, error) {
	// Get both global and local packages
	globalPkgs, err := m.globalPackages()
	if err != nil {
		return nil, err
	}

	// For local packages, we'd need to scan project directories
	// This is more complex and might be added later
	return globalPkgs, nil
}
