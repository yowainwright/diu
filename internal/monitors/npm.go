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

type NPMMonitor struct {
	*ProcessMonitor
	globalPath string
	npmPath    string
}

func NewNPMMonitor() Monitor {
	return &NPMMonitor{
		ProcessMonitor: NewProcessMonitor("npm", "npm"),
	}
}

func (m *NPMMonitor) Initialize(config *core.Config) error {
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}

	// Find npm binary
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found: %w", err)
	}
	m.npmPath = npmPath

	// Get global packages path
	m.globalPath = m.getGlobalPath()

	return nil
}

func (m *NPMMonitor) getGlobalPath() string {
	cmd := exec.Command(m.npmPath, "config", "get", "prefix")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to common locations
		homeDir, _ := os.UserHomeDir()
		return filepath.Join(homeDir, ".npm")
	}

	prefix := strings.TrimSpace(string(output))
	return filepath.Join(prefix, "lib", "node_modules")
}

func (m *NPMMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := &core.ExecutionRecord{
		Tool:     "npm",
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}

	if len(args) == 0 {
		return record, nil
	}

	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand

	// Check if it's a global operation
	isGlobal := contains(args, "-g") || contains(args, "--global")
	record.Metadata["global"] = isGlobal

	switch subcommand {
	case "install", "i", "add":
		packages := m.extractPackagesFromNPMArgs(args[1:])
		record.PackagesAffected = packages
		record.Metadata["action"] = "install"

		// Check for save flags
		if contains(args, "--save-dev") || contains(args, "-D") {
			record.Metadata["dev_dependency"] = true
		}
		if contains(args, "--save-optional") || contains(args, "-O") {
			record.Metadata["optional_dependency"] = true
		}

	case "uninstall", "remove", "rm", "r", "un":
		packages := m.extractPackagesFromNPMArgs(args[1:])
		record.PackagesAffected = packages
		record.Metadata["action"] = "uninstall"

	case "update", "up", "upgrade":
		packages := m.extractPackagesFromNPMArgs(args[1:])
		if len(packages) > 0 {
			record.PackagesAffected = packages
		} else {
			record.Metadata["update_all"] = true
		}

	case "list", "ls", "la", "ll":
		record.Metadata["action"] = "list"
		depth := m.extractDepth(args)
		if depth >= 0 {
			record.Metadata["depth"] = depth
		}

	case "search", "s", "se", "find":
		if len(args) > 1 {
			record.Metadata["search_term"] = strings.Join(args[1:], " ")
		}

	case "run", "run-script":
		if len(args) > 1 {
			record.Metadata["script"] = args[1]
		}

	case "test", "t", "tst":
		record.Metadata["action"] = "test"

	case "start":
		record.Metadata["action"] = "start"

	case "build":
		record.Metadata["action"] = "build"

	case "publish":
		record.Metadata["action"] = "publish"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			record.PackagesAffected = []string{args[1]}
		}

	case "link", "ln":
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			record.PackagesAffected = []string{args[1]}
		}
		record.Metadata["action"] = "link"

	case "audit":
		record.Metadata["action"] = "audit"
		if contains(args, "--fix") {
			record.Metadata["fix"] = true
		}

	case "fund":
		record.Metadata["action"] = "fund"

	case "outdated":
		record.Metadata["action"] = "outdated"
	}

	return record, nil
}

func (m *NPMMonitor) extractPackagesFromNPMArgs(args []string) []string {
	var packages []string
	skipNext := false

	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		// Skip flags
		if strings.HasPrefix(arg, "-") {
			// Some flags take values
			if arg == "--registry" || arg == "--scope" || arg == "--tag" {
				skipNext = true
			}
			continue
		}

		// Parse package specs (name, name@version, @scope/name, etc.)
		if strings.Contains(arg, "@") {
			// Could be scoped package or version spec
			if strings.HasPrefix(arg, "@") {
				// Scoped package
				parts := strings.SplitN(arg, "@", 3)
				if len(parts) >= 2 {
					packages = append(packages, "@"+parts[1])
				}
			} else {
				// Regular package with version
				parts := strings.SplitN(arg, "@", 2)
				packages = append(packages, parts[0])
			}
		} else {
			packages = append(packages, arg)
		}
	}

	return packages
}

func (m *NPMMonitor) extractDepth(args []string) int {
	for i, arg := range args {
		if arg == "--depth" && i+1 < len(args) {
			var depth int
			if _, err := fmt.Sscanf(args[i+1], "%d", &depth); err == nil {
				return depth
			}
		}
	}
	return -1
}

func (m *NPMMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	if m.config.Tools.NPM.TrackGlobalOnly {
		return m.getGlobalPackages()
	}
	return m.getAllPackages()
}

func (m *NPMMonitor) getGlobalPackages() ([]*core.PackageInfo, error) {
	cmd := exec.Command(m.npmPath, "list", "-g", "--depth=0", "--json")
	output, err := cmd.Output()
	if err != nil {
		// npm list might return non-zero on some warnings
		if len(output) == 0 {
			return nil, fmt.Errorf("failed to list global packages: %w", err)
		}
	}

	var listData struct {
		Dependencies map[string]struct {
			Version      string                 `json:"version"`
			Resolved     string                 `json:"resolved"`
			Dependencies map[string]interface{} `json:"dependencies"`
		} `json:"dependencies"`
	}

	if err := json.Unmarshal(output, &listData); err != nil {
		// Fallback to simple parsing
		return m.getGlobalPackagesSimple()
	}

	var packages []*core.PackageInfo
	for name, info := range listData.Dependencies {
		pkg := &core.PackageInfo{
			Name:        name,
			Version:     info.Version,
			Tool:        "npm",
			InstallDate: time.Now(), // NPM doesn't track install time
		}

		// Extract dependencies if available
		if info.Dependencies != nil {
			deps := make([]string, 0, len(info.Dependencies))
			for dep := range info.Dependencies {
				deps = append(deps, dep)
			}
			pkg.Dependencies = deps
		}

		packages = append(packages, pkg)
	}

	return packages, nil
}

func (m *NPMMonitor) getGlobalPackagesSimple() ([]*core.PackageInfo, error) {
	cmd := exec.Command(m.npmPath, "list", "-g", "--depth=0")
	output, err := cmd.Output()
	if err != nil && len(output) == 0 {
		return nil, err
	}

	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		// Skip the first line (npm path) and tree characters
		if strings.Contains(line, "node_modules") || strings.HasPrefix(line, "├") || strings.HasPrefix(line, "└") {
			// Extract package name from lines like "├── package@version"
			line = strings.TrimPrefix(line, "├── ")
			line = strings.TrimPrefix(line, "└── ")
			line = strings.TrimPrefix(line, "├─┬ ")
			line = strings.TrimPrefix(line, "└─┬ ")

			if line != "" {
				parts := strings.Split(line, "@")
				if len(parts) > 0 {
					name := parts[0]
					version := ""
					if len(parts) > 1 {
						version = parts[len(parts)-1]
					}

					pkg := &core.PackageInfo{
						Name:        name,
						Version:     version,
						Tool:        "npm",
						InstallDate: time.Now(),
					}
					packages = append(packages, pkg)
				}
			}
		}
	}

	return packages, nil
}

func (m *NPMMonitor) getAllPackages() ([]*core.PackageInfo, error) {
	// Get both global and local packages
	globalPkgs, err := m.getGlobalPackages()
	if err != nil {
		return nil, err
	}

	// For local packages, we'd need to scan project directories
	// This is more complex and might be added later
	return globalPkgs, nil
}

func (m *NPMMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}