package monitors

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		switch arg {
		case ".", "./...", "...":
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		packages = append(packages, arg)
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
	return m.getBinaries()
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
		if info.Mode()&core.ExecutableModeMask == 0 {
			continue
		}

		pkg := &core.PackageInfo{
			Name:        entry.Name(),
			Tool:        core.ToolGoBinary,
			InstallDate: info.ModTime(),
			Path:        filepath.Join(m.goBin, entry.Name()),
			SizeBytes:   info.Size(),
			ModifiedAt:  info.ModTime().UnixNano(),
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
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(info.Main.Version)
	if version == "" || version == "(devel)" {
		return "", fmt.Errorf("version not found")
	}
	return version, nil
}

func (m *GoMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return m.ProcessMonitor.Start(ctx, eventChan)
}
