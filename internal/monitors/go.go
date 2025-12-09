package monitors

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type GoMonitor struct {
	*ProcessMonitor
	goPath string
	goBin  string
}

func NewGoMonitor() Monitor {
	return &GoMonitor{
		ProcessMonitor: NewProcessMonitor(core.ToolGo, "go"),
	}
}

func (m *GoMonitor) Initialize(config *core.Config) error {
	if err := m.ProcessMonitor.Initialize(config); err != nil {
		return err
	}

	m.goPath = config.Tools.Go.GoPath
	if m.goPath == "" {
		m.goPath = os.Getenv("GOPATH")
	}
	if m.goPath == "" {
		homeDir, _ := os.UserHomeDir()
		m.goPath = filepath.Join(homeDir, "go")
	}

	m.goBin = config.Tools.Go.GoBin
	if m.goBin == "" {
		m.goBin = os.Getenv("GOBIN")
	}
	if m.goBin == "" {
		m.goBin = filepath.Join(m.goPath, "bin")
	}

	return nil
}

func (m *GoMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := &core.ExecutionRecord{
		Tool:     core.ToolGo,
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
	case "get":
		packages := m.extractGoPackages(args[1:])
		record.PackagesAffected = packages
		record.Metadata["action"] = "get"

		// Check for update flag
		if contains(args, "-u") {
			record.Metadata["update"] = true
		}

	case "install":
		packages := m.extractGoPackages(args[1:])
		record.PackagesAffected = packages
		record.Metadata["action"] = "install"

	case "mod":
		if len(args) > 1 {
			modCmd := args[1]
			record.Metadata["mod_command"] = modCmd
			switch modCmd {
			case "download":
				record.Metadata["action"] = "mod_download"
			case "tidy":
				record.Metadata["action"] = "mod_tidy"
			case "vendor":
				record.Metadata["action"] = "mod_vendor"
			case "init":
				if len(args) > 2 {
					record.Metadata["module"] = args[2]
				}
			}
		}

	case "build":
		record.Metadata["action"] = "build"
		if output := m.extractOutputFlag(args); output != "" {
			record.Metadata["output"] = output
		}

	case "run":
		record.Metadata["action"] = "run"
		if len(args) > 1 && strings.HasSuffix(args[1], ".go") {
			record.Metadata["file"] = args[1]
		}

	case "test":
		record.Metadata["action"] = "test"
		packages := m.extractGoPackages(args[1:])
		if len(packages) > 0 {
			record.PackagesAffected = packages
		}

	case "fmt":
		record.Metadata["action"] = "fmt"

	case "vet":
		record.Metadata["action"] = "vet"

	case "list":
		record.Metadata["action"] = "list"
		if contains(args, "-m") {
			record.Metadata["modules"] = true
		}

	case "clean":
		record.Metadata["action"] = "clean"
		if contains(args, "-modcache") {
			record.Metadata["modcache"] = true
		}

	case "env":
		record.Metadata["action"] = "env"

	case "version":
		record.Metadata["action"] = "version"
	}

	return record, nil
}

func (m *GoMonitor) extractGoPackages(args []string) []string {
	var packages []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// Go packages typically look like domain.com/user/package
		if strings.Contains(arg, "/") || strings.Contains(arg, ".") {
			// Extract package name from full path
			parts := strings.Split(arg, "/")
			if len(parts) > 0 {
				// Use the last part as the package name
				pkgName := parts[len(parts)-1]
				// Remove version suffix if present
				if idx := strings.Index(pkgName, "@"); idx > 0 {
					pkgName = pkgName[:idx]
				}
				packages = append(packages, arg)
			}
		} else if arg == "." || arg == "./..." || arg == "..." {
			// Current directory packages
			continue
		} else {
			packages = append(packages, arg)
		}
	}
	return packages
}

func (m *GoMonitor) extractOutputFlag(args []string) string {
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "-o=") {
			return strings.TrimPrefix(arg, "-o=")
		}
	}
	return ""
}

func (m *GoMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	var packages []*core.PackageInfo

	// Get modules if in module mode
	if modPackages, err := m.getModules(); err == nil {
		packages = append(packages, modPackages...)
	}

	// Get binaries in GOBIN
	if binPackages, err := m.getBinaries(); err == nil {
		packages = append(packages, binPackages...)
	}

	return packages, nil
}

func (m *GoMonitor) getModules() ([]*core.PackageInfo, error) {
	cmd := exec.Command("go", "list", "-m", "all")
	output, err := cmd.Output()
	if err != nil {
		// Might not be in a module
		return nil, nil
	}

	var packages []*core.PackageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		name := parts[0]
		version := ""
		if len(parts) > 1 {
			version = parts[1]
		}

		// Skip the main module (first line without version)
		if version == "" && strings.Count(line, " ") == 0 {
			continue
		}

		pkg := &core.PackageInfo{
			Name:        name,
			Version:     version,
			Tool:        core.ToolGo,
			InstallDate: time.Now(),
		}
		packages = append(packages, pkg)
	}

	return packages, nil
}

func (m *GoMonitor) getBinaries() ([]*core.PackageInfo, error) {
	if m.goBin == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(m.goBin)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read GOBIN: %w", err)
	}

	var packages []*core.PackageInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Check if executable
		if info.Mode()&0111 == 0 {
			continue
		}

		pkg := &core.PackageInfo{
			Name:        entry.Name(),
			Tool:        core.ToolGoBinary,
			InstallDate: info.ModTime(),
			Path:        filepath.Join(m.goBin, entry.Name()),
		}

		// Try to get version
		if version, err := m.getBinaryVersion(pkg.Path); err == nil {
			pkg.Version = version
		}

		packages = append(packages, pkg)
	}

	return packages, nil
}

func (m *GoMonitor) getBinaryVersion(binaryPath string) (string, error) {
	cmd := exec.Command(binaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		// Try --version flag
		cmd = exec.Command(binaryPath, "--version")
		output, err = cmd.Output()
		if err != nil {
			return "", err
		}
	}

	// Extract version from output
	outputStr := strings.TrimSpace(string(output))
	lines := strings.Split(outputStr, "\n")
	if len(lines) > 0 {
		// Look for version patterns
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), "version") {
				parts := strings.Fields(line)
				for _, part := range parts {
					if strings.HasPrefix(part, "v") || strings.Contains(part, ".") {
						return part, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("version not found")
}

func (m *GoMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}