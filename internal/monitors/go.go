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
	m.goPath = goPath(config)
	m.goBin = goBin(config, m.goPath)
	return nil
}

func goPath(config *core.Config) string {
	path := config.Tools.Go.GoPath
	if path == "" {
		path = os.Getenv("GOPATH")
	}
	if path == "" {
		homeDir, _ := os.UserHomeDir()
		path = filepath.Join(homeDir, "go")
	}
	return path
}

func goBin(config *core.Config, path string) string {
	bin := config.Tools.Go.GoBin
	if bin == "" {
		bin = os.Getenv("GOBIN")
	}
	if bin == "" {
		bin = filepath.Join(path, "bin")
	}
	return bin
}

func (m *GoMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	record := newGoExecutionRecord(cmd, args)
	if len(args) == 0 {
		return record, nil
	}
	subcommand := args[0]
	record.Metadata["subcommand"] = subcommand
	m.applyGoSubcommand(record, subcommand, args)
	return record, nil
}

func newGoExecutionRecord(cmd string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:     core.ToolGo,
		Command:  cmd,
		Args:     args,
		Metadata: make(map[string]interface{}),
	}
}

func (m *GoMonitor) applyGoSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "get":
		m.applyGoGet(record, args)
	case "install":
		m.applyGoInstall(record, args)
	case "mod":
		applyGoModMetadata(record, args)
	default:
		m.applyGoToolSubcommand(record, subcommand, args)
	}
}

func (m *GoMonitor) applyGoToolSubcommand(record *core.ExecutionRecord, subcommand string, args []string) {
	switch subcommand {
	case "build":
		m.applyGoBuild(record, args)
	case "run":
		applyGoRun(record, args)
	case "test":
		m.applyGoTest(record, args)
	case "fmt", "vet":
		applyGoSimpleAction(record, subcommand)
	case "list":
		applyGoList(record, args)
	case "clean":
		applyGoClean(record, args)
	case "env", "version":
		applyGoSimpleAction(record, subcommand)
	}
}

func applyGoSimpleAction(record *core.ExecutionRecord, action string) {
	record.Metadata["action"] = action
}

func (m *GoMonitor) applyGoGet(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractGoPackages(args[1:])
	record.Metadata["action"] = "get"
	if contains(args, "-u") {
		record.Metadata["update"] = true
	}
}

func (m *GoMonitor) applyGoInstall(record *core.ExecutionRecord, args []string) {
	record.PackagesAffected = m.extractGoPackages(args[1:])
	record.Metadata["action"] = "install"
}

func (m *GoMonitor) applyGoBuild(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "build"
	if output := m.extractOutputFlag(args); output != "" {
		record.Metadata["output"] = output
	}
}

func applyGoRun(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "run"
	hasFileArg := len(args) > 1
	isGoFile := hasFileArg && strings.HasSuffix(args[1], ".go")
	if isGoFile {
		record.Metadata["file"] = args[1]
	}
}

func (m *GoMonitor) applyGoTest(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "test"
	packages := m.extractGoPackages(args[1:])
	if len(packages) > 0 {
		record.PackagesAffected = packages
	}
}

func applyGoList(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "list"
	if contains(args, "-m") {
		record.Metadata["modules"] = true
	}
}

func applyGoClean(record *core.ExecutionRecord, args []string) {
	record.Metadata["action"] = "clean"
	if contains(args, "-modcache") {
		record.Metadata["modcache"] = true
	}
}

func applyGoModMetadata(record *core.ExecutionRecord, args []string) {
	if len(args) <= 1 {
		return
	}
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
		recordGoModule(record, args)
	}
}

func recordGoModule(record *core.ExecutionRecord, args []string) {
	if len(args) > 2 {
		record.Metadata["module"] = args[2]
	}
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
		hasOutputFlag := arg == "-o"
		hasValue := i+1 < len(args)
		hasOutputValue := hasOutputFlag && hasValue
		if hasOutputValue {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "-o=") {
			return strings.TrimPrefix(arg, "-o=")
		}
	}
	return ""
}

//nolint:legibility // Monitor interface requires this method name.
func (m *GoMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return m.binaries()
}

func (m *GoMonitor) binaries() ([]*core.PackageInfo, error) {
	entries, err := m.goBinEntries()
	if err != nil {
		return nil, err
	}
	return m.goBinaryPackages(entries), nil
}

func (m *GoMonitor) goBinEntries() ([]os.DirEntry, error) {
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
	return entries, nil
}

func (m *GoMonitor) goBinaryPackages(entries []os.DirEntry) []*core.PackageInfo {
	var packages []*core.PackageInfo
	for _, entry := range entries {
		pkg, ok := m.goBinaryPackage(entry)
		if !ok {
			continue
		}
		packages = append(packages, pkg)
	}
	return packages
}

func (m *GoMonitor) goBinaryPackage(entry os.DirEntry) (*core.PackageInfo, bool) {
	if entry.IsDir() {
		return nil, false
	}
	info, err := entry.Info()
	if err != nil {
		return nil, false
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return nil, false
	}
	pkg := m.newGoBinaryPackage(entry.Name(), info)
	m.addGoBinaryVersion(pkg)
	return pkg, true
}

func (m *GoMonitor) newGoBinaryPackage(name string, info os.FileInfo) *core.PackageInfo {
	return &core.PackageInfo{
		Name:        name,
		Tool:        core.ToolGoBinary,
		InstallDate: info.ModTime(),
		Path:        filepath.Join(m.goBin, name),
		SizeBytes:   info.Size(),
		ModifiedAt:  info.ModTime().UnixNano(),
	}
}

func (m *GoMonitor) addGoBinaryVersion(pkg *core.PackageInfo) {
	if version, err := m.binaryVersion(pkg.Path); err == nil {
		pkg.Version = version
	}
}

func (m *GoMonitor) binaryVersion(binaryPath string) (string, error) {
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(info.Main.Version)
	hasVersion := version != ""
	isReleasedVersion := version != "(devel)"
	validVersion := hasVersion && isReleasedVersion
	if !validVersion {
		return "", fmt.Errorf("version not found")
	}
	return version, nil
}

func (m *GoMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	err := m.ProcessMonitor.Start(ctx, eventChan)
	return err
}
