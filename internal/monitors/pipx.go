package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func NewPipxMonitor() Monitor {
	return &PipxMonitor{BaseMonitor: NewBaseMonitor(core.ToolPipx)}
}

func (m *PipxMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(core.ToolPipx); err != nil {
		return fmt.Errorf("pipx not found: %w", err)
	}
	return m.BaseMonitor.Initialize(config)
}

func (m *PipxMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return newExecutionRecord(core.ToolPipx, cmd, args), nil
}

//nolint:legibility // Monitor interface requires this method name.
func (m *PipxMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	snapshot, err := readPipxSnapshot()
	if err != nil {
		return nil, err
	}
	var packages []*core.PackageInfo
	for _, name := range slices.Sorted(maps.Keys(snapshot.Venvs)) {
		main := snapshot.Venvs[name].Metadata.MainPackage
		pkg, err := pipxPackageInfo(name, main)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func readPipxSnapshot() (*pipxSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, core.ToolPipx, "list", "--json").Output()
	if err != nil {
		return nil, globalPackageListError(core.ToolPipx, err)
	}
	var snapshot pipxSnapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return nil, fmt.Errorf("invalid pipx inventory: %w", err)
	}
	valid := snapshot.Version == "0.1" && snapshot.Venvs != nil
	if !valid {
		return nil, fmt.Errorf("invalid pipx inventory schema")
	}
	return &snapshot, nil
}

func PipxExecutables() (map[string]*core.PackageInfo, error) {
	snapshot, err := readPipxSnapshot()
	if err != nil {
		return nil, err
	}
	binaries := make(map[string]*core.PackageInfo)
	for _, name := range slices.Sorted(maps.Keys(snapshot.Venvs)) {
		metadata := snapshot.Venvs[name].Metadata
		if err := addPipxEnvironmentExecutables(binaries, name, metadata); err != nil {
			return nil, err
		}
	}
	return binaries, nil
}

func addPipxEnvironmentExecutables(binaries map[string]*core.PackageInfo, name string, metadata pipxMetadata) error {
	pkg, err := pipxPackageInfo(name, metadata.MainPackage)
	if err != nil {
		return err
	}
	isHidden := metadata.ExposureEnabled != nil && !*metadata.ExposureEnabled
	if isHidden {
		return nil
	}
	packages := append([]*pipxPackage{metadata.MainPackage}, slices.Collect(maps.Values(metadata.InjectedPackages))...)
	for _, member := range packages {
		if err := validatePipxPackage(name, member); err != nil {
			return err
		}
		if err := addPipxPackageExecutables(binaries, member, pkg); err != nil {
			return err
		}
	}
	return nil
}

func addPipxPackageExecutables(binaries map[string]*core.PackageInfo, member *pipxPackage, pkg *core.PackageInfo) error {
	for _, path := range pipxExposedPaths(member) {
		if !validPipxInstallPath(pkg.Name, path) {
			return fmt.Errorf("invalid pipx exposed command for %q: %s", pkg.Name, path.Path)
		}
		name := filepath.Base(path.Path) + member.Suffix
		if !validPipxName(name) {
			return fmt.Errorf("invalid pipx exposed command: %q", name)
		}
		setPipxExecutable(binaries, name, path.Path, pkg)
		setLegacyPipxExecutable(binaries, path.Path, member.Suffix, pkg)
	}
	return nil
}

func setLegacyPipxExecutable(binaries map[string]*core.PackageInfo, path, suffix string, pkg *core.PackageInfo) {
	base := filepath.Base(path)
	extension := filepath.Ext(base)
	name := strings.TrimSuffix(base, extension) + suffix + extension
	isLegacy := name != base+suffix && pipxCommandIsActive(name, path)
	if isLegacy {
		setPipxExecutable(binaries, name, path, pkg)
	}
}

func setPipxExecutable(binaries map[string]*core.PackageInfo, name, path string, pkg *core.PackageInfo) {
	shouldUse := binaries[name] == nil || pipxCommandIsActive(name, path)
	if !shouldUse {
		return
	}
	binary := *pkg
	binary.Path = path
	binaries[name] = &binary
}

func pipxCommandIsActive(name, path string) bool {
	active, err := exec.LookPath(name)
	if err != nil {
		return false
	}
	activeInfo, err := os.Stat(active)
	if err != nil {
		return false
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(activeInfo, pathInfo)
}

func pipxExposedPaths(pkg *pipxPackage) []pipxPath {
	var paths []pipxPath
	if pkg.ShouldIncludeApps {
		paths = append(paths, pkg.AppPaths...)
	}
	for _, dependency := range slices.Sorted(maps.Keys(pkg.DependencyPaths)) {
		isExposed := pkg.ShouldIncludeDependencies || slices.Contains(pkg.IncludeResources, dependency)
		if isExposed {
			paths = append(paths, pkg.DependencyPaths[dependency]...)
		}
	}
	return paths
}

func pipxPackageInfo(name string, main *pipxPackage) (*core.PackageInfo, error) {
	if err := validatePipxPackage(name, main); err != nil {
		return nil, err
	}
	path, err := pipxInstallPath(name, main)
	if err != nil {
		return nil, err
	}
	return &core.PackageInfo{Name: name, Tool: core.ToolPipx, Version: main.Version, Path: path}, nil
}

func pipxInstallPath(name string, main *pipxPackage) (string, error) {
	candidates := main.AppPaths
	if len(candidates) == 0 {
		candidates = pipxExposedPaths(main)
	}
	var paths []string
	for _, path := range candidates {
		if !validPipxInstallPath(name, path) {
			return "", fmt.Errorf("invalid pipx install path for %q: %s", name, path.Path)
		}
		paths = append(paths, path.Path)
	}
	if len(paths) == 0 {
		return "", nil
	}
	return slices.Min(paths), nil
}

func validatePipxPackage(name string, main *pipxPackage) error {
	if main == nil {
		return fmt.Errorf("pipx install %q has no main package", name)
	}
	validIdentity := validPipxName(name) && validPipxName(main.Package)
	hasVersion := strings.TrimSpace(main.Version) != ""
	validPackage := validIdentity && hasVersion
	if !validPackage {
		return fmt.Errorf("invalid pipx install identity: %q", name)
	}
	if len(main.Apps) != len(main.AppPaths) {
		return fmt.Errorf("pipx install %q has incomplete command metadata", name)
	}
	for _, path := range main.AppPaths {
		if !validPipxPath(name, main, path) {
			return fmt.Errorf("invalid pipx command path for %q: %s", name, path.Path)
		}
	}
	return nil
}

func validPipxName(name string) bool {
	valid, _ := regexp.MatchString(`^[A-Za-z0-9_][A-Za-z0-9_.@+-]*$`, name)
	unsafe := strings.Contains(name, "..")
	isSafe := valid && !unsafe
	return isSafe
}

func validPipxPath(install string, pkg *pipxPackage, path pipxPath) bool {
	name := filepath.Base(path.Path)
	validName := validPipxName(name+pkg.Suffix) && slices.Contains(pkg.Apps, name)
	return validName && validPipxInstallPath(install, path)
}

func validPipxInstallPath(install string, path pipxPath) bool {
	bin := filepath.Dir(path.Path)
	venv := filepath.Dir(bin)
	isAbsolute := filepath.IsAbs(path.Path) && filepath.Clean(path.Path) == path.Path
	isVenv := filepath.Base(bin) == "bin" && filepath.Base(venv) == install
	insidePipx := filepath.Base(filepath.Dir(venv)) == "venvs"
	isOwned := isAbsolute && isVenv && insidePipx
	isValid := path.Type == "Path" && isOwned
	return isValid
}
