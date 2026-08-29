package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

const (
	homebrewCommandName = "brew"

	homebrewCellarFlag = "--cellar"
	homebrewPrefixFlag = "--prefix"
	homebrewInfoCmd    = "info"
	homebrewInstalled  = "--installed"
	homebrewJSONV2Arg  = "--json=v2"
	homebrewListCmd    = "list"
	homebrewFormulaArg = "--formula"
	homebrewCaskArg    = "--cask"
	homebrewVersions   = "--versions"

	homebrewCaskTool = core.ToolHomebrewCask
)

type HomebrewMonitor struct {
	*ProcessMonitor
	cellarPaths []string
	caskroom    string
}

func NewHomebrewMonitor() Monitor {
	return &HomebrewMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolHomebrew, "brew"),
	}
}

func (m *HomebrewMonitor) Initialize(config *core.Config) error {
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}

	m.cellarPaths = config.Tools.Homebrew.CellarPaths
	if len(m.cellarPaths) == 0 {
		m.cellarPaths = m.detectCellarPaths()
	}

	m.caskroom = m.detectCaskroom()
	return nil
}

func (m *HomebrewMonitor) detectCellarPaths() []string {
	paths := existingDirectories(defaultCellarPaths())
	return appendUniquePath(paths, homebrewCellarPath())
}

func defaultCellarPaths() []string {
	homeDir, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/Cellar",
		"/usr/local/Cellar",
		filepath.Join(homeDir, "homebrew/Cellar"),
	}
}

func existingDirectories(paths []string) []string {
	var existing []string
	for _, path := range paths {
		if directoryExists(path) {
			existing = append(existing, path)
		}
	}
	return existing
}

func homebrewCellarPath() string {
	if _, err := exec.LookPath(homebrewCommandName); err == nil {
		if output, err := exec.Command(homebrewCommandName, homebrewCellarFlag).Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	}
	return ""
}

func (m *HomebrewMonitor) detectCaskroom() string {
	if path := firstExistingDirectory(defaultCaskroomPaths()); path != "" {
		return path
	}
	return homebrewCaskroomPath()
}

func defaultCaskroomPaths() []string {
	homeDir, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/Caskroom",
		"/usr/local/Caskroom",
		filepath.Join(homeDir, "homebrew/Caskroom"),
	}
}

func firstExistingDirectory(paths []string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if directoryExists(path) {
			return path
		}
	}
	return ""
}

func homebrewCaskroomPath() string {
	if _, err := exec.LookPath(homebrewCommandName); err == nil {
		if output, err := exec.Command(homebrewCommandName, homebrewPrefixFlag).Output(); err == nil {
			prefix := strings.TrimSpace(string(output))
			caskroom := filepath.Join(prefix, "Caskroom")
			if directoryExists(caskroom) {
				return caskroom
			}
		}
	}
	return ""
}

func directoryExists(path string) bool {
	info, err := safefs.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func appendUniquePath(paths []string, path string) []string {
	if path == "" {
		return paths
	}
	if contains(paths, path) {
		return paths
	}
	return append(paths, path)
}

func (m *HomebrewMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newHomebrewExecutionRecord(cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	m.applyHomebrewSubcommand(record, subcommand, args)
	return record, nil
}

func newHomebrewExecutionRecord(cmd string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:     core.ToolHomebrew,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
}

func (m *HomebrewMonitor) applyHomebrewSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "install", "uninstall", "remove", "rm", "upgrade", "reinstall":
		m.applyHomebrewPackageSubcommand(record, subcommand, args)
	default:
		applyHomebrewMetadataSubcommand(record, subcommand, args)
	}
}

func (m *HomebrewMonitor) applyHomebrewPackageSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "install":
		m.applyHomebrewInstall(record, args)
	case "uninstall", "remove", "rm":
		m.applyHomebrewUninstall(record, args)
	case "upgrade":
		m.applyHomebrewUpgrade(record, args)
	default:
		m.applyHomebrewReinstall(record, args)
	}
}

func applyHomebrewMetadataSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "tap":
		setHomebrewMetadataArg(record, "tap", args)
	case "untap":
		setHomebrewMetadataArg(record, "untap", args)
	case "list", "ls":
		record.Metadata["action"] = "list"
	case "search":
		setHomebrewSearchTerm(record, args)
	case "info":
		setHomebrewInfoPackage(record, args)
	case "services":
		setHomebrewServiceMetadata(record, args)
	}
}

func (m *HomebrewMonitor) applyHomebrewInstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
	if contains(args, "--cask") {
		record.Metadata["type"] = "cask"
		return
	}
	record.Metadata["type"] = "formula"
}

func (m *HomebrewMonitor) applyHomebrewUninstall(record *core.ExecutionRecord, args []string) {
	skipFlags := []string{"--cask", "--formula", "--force", "--ignore-dependencies"}
	record.PackagesAffected = m.extractPackagesFromArgs(args[1:], skipFlags)
	record.Metadata["action"] = "uninstall"
	setHomebrewPackageType(record, args)
}

func (m *HomebrewMonitor) applyHomebrewUpgrade(record *core.ExecutionRecord, args []string) {
	setHomebrewPackageType(record, args)
	if homebrewUpgradesAll(args) {
		record.Metadata["upgrade_all"] = true
		return
	}
	record.PackagesAffected = m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
}

func (m *HomebrewMonitor) applyHomebrewReinstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
	record.Metadata["action"] = "reinstall"
	setHomebrewPackageType(record, args)
}

func setHomebrewMetadataArg(record *core.ExecutionRecord, key string, args []string) {
	if len(args) <= 1 {
		return
	}
	record.Metadata[key] = args[1]
}

func setHomebrewSearchTerm(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
	record.Metadata["search_term"] = strings.Join(args[1:], " ")
}

func setHomebrewInfoPackage(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
	record.PackagesAffected = []string{args[1]}
}

func setHomebrewServiceMetadata(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
	record.Metadata["service_action"] = args[1]
	if len(args) > 2 {
		record.PackagesAffected = []string{args[2]}
	}
}

func homebrewUpgradesAll(args []string) bool {
	if len(args) <= 1 {
		return true
	}
	return strings.HasPrefix(args[1], "-")
}

func setHomebrewPackageType(record *core.ExecutionRecord, args []string) {
	if contains(args, "--cask") {
		record.Metadata["type"] = "cask"
		return
	}
	if contains(args, "--formula") {
		record.Metadata["type"] = "formula"
	}
}

func (m *HomebrewMonitor) extractPackagesFromArgs(args []string, flags []string) []string {
	var packages []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !contains(flags, arg) {
			packages = append(packages, arg)
		}
	}
	return packages
}

//nolint:legibility // Monitor interface requires this method name.
func (m *HomebrewMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	packages, err := m.formulae()
	if err != nil {
		return nil, err
	}
	if m.config.Tools.Homebrew.TrackCasks {
		casks, err := m.casks()
		if err != nil {
			return nil, err
		}
		packages = append(packages, casks...)
	}
	return packages, nil
}

func (m *HomebrewMonitor) formulae() ([]*core.PackageInfo, error) {
	cmd := exec.Command(homebrewCommandName, homebrewListCmd, homebrewFormulaArg, homebrewVersions)
	packages, err := m.listPackages(cmd, core.ToolHomebrew)
	if err == nil {
		return packages, nil
	}
	installed, err := m.installedInfo()
	if err != nil {
		return nil, err
	}
	return installed.formulaPackages(), nil
}

func (m *HomebrewMonitor) casks() ([]*core.PackageInfo, error) {
	cmd := exec.Command(homebrewCommandName, homebrewListCmd, homebrewCaskArg, homebrewVersions)
	packages, err := m.listPackages(cmd, homebrewCaskTool)
	if err == nil {
		return packages, nil
	}
	installed, err := m.installedInfo()
	if err != nil {
		return nil, err
	}
	return installed.caskPackages(), nil
}

func (m *HomebrewMonitor) listPackages(cmd *exec.Cmd, tool string) ([]*core.PackageInfo, error) {
	if _, err := exec.LookPath(homebrewCommandName); err != nil {
		return nil, fmt.Errorf("brew not found: %w", err)
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list Homebrew packages: %w", err)
	}
	return parseHomebrewPackageList(output, tool)
}

func parseHomebrewPackageList(output []byte, tool string) ([]*core.PackageInfo, error) {
	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		version := ""
		if len(fields) > 1 {
			version = fields[len(fields)-1]
		}
		pkg := &core.PackageInfo{Name: fields[0], Version: version, Tool: tool}
		packages = append(packages, pkg)
	}
	return packages, scanner.Err()
}

type homebrewInstalledInfo struct {
	Formulae []homebrewFormula `json:"formulae"`
	Casks    []homebrewCask    `json:"casks"`
}

type homebrewFormula struct {
	Name      string                 `json:"name"`
	Installed []homebrewInstallation `json:"installed"`
}

type homebrewInstallation struct {
	Version string `json:"version"`
	Time    int64  `json:"time"`
}

type homebrewCask struct {
	Token         string `json:"token"`
	Version       string `json:"version"`
	InstalledTime int64  `json:"installed_time"`
}

func (m *HomebrewMonitor) installedInfo() (*homebrewInstalledInfo, error) {
	if _, err := exec.LookPath(homebrewCommandName); err != nil {
		return nil, fmt.Errorf("brew not found: %w", err)
	}

	cmd := exec.Command(homebrewCommandName, homebrewInfoCmd, homebrewJSONV2Arg, homebrewInstalled)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed Homebrew packages: %w", err)
	}

	var installed homebrewInstalledInfo
	if err := json.Unmarshal(output, &installed); err != nil {
		return nil, fmt.Errorf("failed to parse installed Homebrew packages: %w", err)
	}
	return &installed, nil
}

func (i *homebrewInstalledInfo) formulaPackages() []*core.PackageInfo {
	var packages []*core.PackageInfo
	for _, formula := range i.Formulae {
		var version string
		var installDate time.Time
		installed, exists := latestHomebrewInstallation(formula.Installed)
		if exists {
			version = installed.Version
			installDate = time.Unix(installed.Time, 0)
		}
		pkg := &core.PackageInfo{
			Name:        formula.Name,
			Version:     version,
			Tool:        core.ToolHomebrew,
			InstallDate: installDate,
		}
		packages = append(packages, pkg)
	}
	return packages
}

func latestHomebrewInstallation(installed []homebrewInstallation) (homebrewInstallation, bool) {
	if len(installed) == 0 {
		return homebrewInstallation{}, false
	}
	latest := installed[0]
	for _, candidate := range installed[1:] {
		if candidate.Time > latest.Time {
			latest = candidate
		}
	}
	return latest, true
}

func (i *homebrewInstalledInfo) caskPackages() []*core.PackageInfo {
	var packages []*core.PackageInfo
	for _, cask := range i.Casks {
		installDate := time.Time{}
		if cask.InstalledTime > 0 {
			installDate = time.Unix(cask.InstalledTime, 0)
		}
		packages = append(packages, &core.PackageInfo{
			Name:        cask.Token,
			Version:     cask.Version,
			Tool:        homebrewCaskTool,
			InstallDate: installDate,
		})
	}
	return packages
}

func (m *HomebrewMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
