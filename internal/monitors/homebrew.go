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

	candidates := []string{
		"/opt/homebrew/Cellar",
		"/usr/local/Cellar",
		filepath.Join(os.Getenv("HOME"), "homebrew/Cellar"),
	}

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}

	if brewPath, err := exec.LookPath("brew"); err == nil {
		if output, err := exec.Command(brewPath, "--cellar").Output(); err == nil {
			cellar := strings.TrimSpace(string(output))
			if cellar != "" && !contains(paths, cellar) {
				paths = append(paths, cellar)
			}
		}
	}

	return paths
}

func (m *HomebrewMonitor) detectCaskroom() string {
	candidates := []string{
		"/opt/homebrew/Caskroom",
		"/usr/local/Caskroom",
		filepath.Join(os.Getenv("HOME"), "homebrew/Caskroom"),
	}

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}

	if brewPath, err := exec.LookPath("brew"); err == nil {
		if output, err := exec.Command(brewPath, "--prefix").Output(); err == nil {
			prefix := strings.TrimSpace(string(output))
			caskroom := filepath.Join(prefix, "Caskroom")
			if info, err := os.Stat(caskroom); err == nil && info.IsDir() {
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

	case "upgrade":
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
	var packages []*core.PackageInfo

	// Get formulae
	if formulae, err := m.getFormulae(); err == nil {
		packages = append(packages, formulae...)
	}

	// Get casks if configured
	if m.config.Tools.Homebrew.TrackCasks {
		if casks, err := m.getCasks(); err == nil {
			packages = append(packages, casks...)
		}
	}

	return packages, nil
}

func (m *HomebrewMonitor) getFormulae() ([]*core.PackageInfo, error) {
	brewPath, err := exec.LookPath("brew")
	if err != nil {
		return nil, fmt.Errorf("brew not found: %w", err)
	}

	// Get JSON info for all installed formulae
	cmd := exec.Command(brewPath, "list", "--formula", "--json=v2")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list formulae: %w", err)
	}

	var brewData struct {
		Formulae []struct {
			Name            string   `json:"name"`
			FullName        string   `json:"full_name"`
			Version         string   `json:"version"`
			InstalledTime   string   `json:"installed_time"`
			Dependencies    []string `json:"dependencies"`
			InstalledAsPath string   `json:"installed_as_dependency"`
		} `json:"formulae"`
	}

	if err := json.Unmarshal(output, &brewData); err != nil {
		// Fallback to simple list
		return m.getFormulaeSimple()
	}

	var packages []*core.PackageInfo
	for _, formula := range brewData.Formulae {
		installTime, _ := time.Parse(time.RFC3339, formula.InstalledTime)
		pkg := &core.PackageInfo{
			Name:         formula.Name,
			Version:      formula.Version,
			Tool:         core.ToolHomebrew,
			InstallDate:  installTime,
			Dependencies: formula.Dependencies,
		}
		packages = append(packages, pkg)
	}

	return packages, nil
}

func (m *HomebrewMonitor) getFormulaeSimple() ([]*core.PackageInfo, error) {
	brewPath, err := exec.LookPath("brew")
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(brewPath, "list", "--formula")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name != "" {
			pkg := &core.PackageInfo{
				Name:        name,
				Tool:        core.ToolHomebrew,
				InstallDate: time.Now(),
			}
			packages = append(packages, pkg)
		}
	}

	return packages, nil
}

func (m *HomebrewMonitor) getCasks() ([]*core.PackageInfo, error) {
	brewPath, err := exec.LookPath("brew")
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(brewPath, "list", "--cask")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name != "" {
			pkg := &core.PackageInfo{
				Name:        name,
				Tool:        "homebrew-cask",
				InstallDate: time.Now(),
			}
			packages = append(packages, pkg)
		}
	}

	return packages, nil
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