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
	var paths []string
	homeDir, _ := os.UserHomeDir()

	candidates := []string{
		"/opt/homebrew/Cellar",
		"/usr/local/Cellar",
		filepath.Join(homeDir, "homebrew/Cellar"),
	}

	for _, path := range candidates {
		if info, err := safefs.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}

	if _, err := exec.LookPath(homebrewCommandName); err == nil {
		if output, err := exec.Command(homebrewCommandName, homebrewCellarFlag).Output(); err == nil {
			cellar := strings.TrimSpace(string(output))
			if cellar != "" && !contains(paths, cellar) {
				paths = append(paths, cellar)
			}
		}
	}

	return paths
}

func (m *HomebrewMonitor) detectCaskroom() string {
	homeDir, _ := os.UserHomeDir()
	candidates := []string{
		"/opt/homebrew/Caskroom",
		"/usr/local/Caskroom",
		filepath.Join(homeDir, "homebrew/Caskroom"),
	}

	for _, path := range candidates {
		if info, err := safefs.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}

	if _, err := exec.LookPath(homebrewCommandName); err == nil {
		if output, err := exec.Command(homebrewCommandName, homebrewPrefixFlag).Output(); err == nil {
			prefix := strings.TrimSpace(string(output))
			caskroom := filepath.Join(prefix, "Caskroom")
			if info, err := safefs.Stat(caskroom); err == nil && info.IsDir() {
				return caskroom
			}
		}
	}

	return ""
}

func (m *HomebrewMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := &core.ExecutionRecord{
		Tool:     core.ToolHomebrew,
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
		packages := m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
		record.PackagesAffected = packages
		if contains(args, "--cask") {
			record.Metadata["type"] = "cask"
		} else {
			record.Metadata["type"] = "formula"
		}

	case "uninstall", "remove", "rm":
		packages := m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula", "--force", "--ignore-dependencies"})
		record.PackagesAffected = packages
		record.Metadata["action"] = "uninstall"
		setHomebrewPackageType(record, args)

	case "upgrade":
		setHomebrewPackageType(record, args)
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			record.PackagesAffected = m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
		} else {
			// Upgrade all
			record.Metadata["upgrade_all"] = true
		}

	case "reinstall":
		packages := m.extractPackagesFromArgs(args[1:], []string{"--cask", "--formula"})
		record.PackagesAffected = packages
		record.Metadata["action"] = "reinstall"
		setHomebrewPackageType(record, args)

	case "tap":
		if len(args) > 1 {
			record.Metadata["tap"] = args[1]
		}

	case "untap":
		if len(args) > 1 {
			record.Metadata["untap"] = args[1]
		}

	case "list", "ls":
		record.Metadata["action"] = "list"

	case "search":
		if len(args) > 1 {
			record.Metadata["search_term"] = strings.Join(args[1:], " ")
		}

	case "info":
		if len(args) > 1 {
			record.PackagesAffected = []string{args[1]}
		}

	case "services":
		if len(args) > 1 {
			record.Metadata["service_action"] = args[1]
			if len(args) > 2 {
				record.PackagesAffected = []string{args[2]}
			}
		}
	}

	return record, nil
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

func (m *HomebrewMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	packages, err := m.getFormulae()
	if err != nil {
		return nil, err
	}
	if m.config.Tools.Homebrew.TrackCasks {
		casks, err := m.getCasks()
		if err != nil {
			return nil, err
		}
		packages = append(packages, casks...)
	}
	return packages, nil
}

func (m *HomebrewMonitor) getFormulae() ([]*core.PackageInfo, error) {
	cmd := exec.Command(homebrewCommandName, homebrewListCmd, homebrewFormulaArg, homebrewVersions)
	packages, err := m.listPackages(cmd, core.ToolHomebrew)
	if err == nil {
		return packages, nil
	}
	installed, err := m.getInstalledInfo()
	if err != nil {
		return nil, err
	}
	return installed.formulaPackages(), nil
}

func (m *HomebrewMonitor) getCasks() ([]*core.PackageInfo, error) {
	cmd := exec.Command(homebrewCommandName, homebrewListCmd, homebrewCaskArg, homebrewVersions)
	packages, err := m.listPackages(cmd, homebrewCaskTool)
	if err == nil {
		return packages, nil
	}
	installed, err := m.getInstalledInfo()
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

func (m *HomebrewMonitor) getInstalledInfo() (*homebrewInstalledInfo, error) {
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
	return m.ProcessMonitor.Start(ctx, eventChan)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
