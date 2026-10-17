package monitors

import (
	"context"
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

func NewCargoMonitor() Monitor {
	return &CargoMonitor{BaseMonitor: NewBaseMonitor(core.ToolCargo)}
}

func (m *CargoMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath(core.ToolCargo); err != nil {
		return fmt.Errorf("cargo not found: %w", err)
	}
	return m.BaseMonitor.Initialize(config)
}

func (m *CargoMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return newExecutionRecord(core.ToolCargo, cmd, args), nil
}

//nolint:legibility // Monitor interface requires this method name.
func (m *CargoMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	binaries, err := CargoExecutables("")
	if err != nil {
		return nil, err
	}
	return cargoPackages(binaries), nil
}

func cargoPackages(binaries map[string]*core.PackageInfo) []*core.PackageInfo {
	var packages []*core.PackageInfo
	seen := make(map[string]bool)
	for _, name := range slices.Sorted(maps.Keys(binaries)) {
		pkg := binaries[name]
		if !seen[pkg.Name] {
			packages = append(packages, pkg)
			seen[pkg.Name] = true
		}
	}
	return packages
}

func CargoExecutables(root string) (map[string]*core.PackageInfo, error) {
	if root == "" {
		root = os.Getenv("CARGO_INSTALL_ROOT")
	}
	binaries, err := readCargoExecutables(root)
	if err != nil {
		return nil, err
	}
	if root == "" {
		resolveCargoPaths(binaries)
	}
	return binaries, nil
}

func readCargoExecutables(root string) (map[string]*core.PackageInfo, error) {
	args := []string{"install", "--list"}
	if root != "" {
		args = append(args, "--root", root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, core.ToolCargo, args...).Output()
	if err != nil {
		return nil, globalPackageListError(core.ToolCargo, err)
	}
	return parseCargoExecutables(string(output), root)
}

func parseCargoExecutables(output, root string) (map[string]*core.PackageInfo, error) {
	inventory := cargoInventory{binaries: make(map[string]*core.PackageInfo)}
	for line := range strings.Lines(output) {
		if err := inventory.addLine(strings.TrimRight(line, "\r\n"), root); err != nil {
			return nil, err
		}
	}
	if err := inventory.finishPackage(); err != nil {
		return nil, err
	}
	return inventory.binaries, nil
}

func (i *cargoInventory) addLine(line, root string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if strings.HasPrefix(line, "    ") {
		return i.addBinary(strings.TrimSpace(line), root)
	}
	if err := i.finishPackage(); err != nil {
		return err
	}
	pkg, err := parseCargoHeader(line)
	i.current, i.hasBins = pkg, false
	return err
}

func (i *cargoInventory) finishPackage() error {
	if i.current == nil {
		return nil
	}
	if !i.hasBins {
		return fmt.Errorf("cargo package %q has no binaries", i.current.Name)
	}
	return nil
}

func parseCargoHeader(line string) (*core.PackageInfo, error) {
	fields := strings.Fields(strings.TrimSuffix(line, ":"))
	validHeader := len(fields) >= 2 && strings.HasSuffix(line, ":")
	if !validHeader {
		return nil, fmt.Errorf("invalid cargo package: %q", line)
	}
	name, version := fields[0], strings.TrimPrefix(fields[1], "v")
	hasVersion := strings.HasPrefix(fields[1], "v") && validCargoVersion(version)
	validPackage := validCargoName(name) && hasVersion
	if !validPackage {
		return nil, fmt.Errorf("invalid cargo package: %q", line)
	}
	return &core.PackageInfo{Name: name, Version: version, Tool: core.ToolCargo}, nil
}

func (i *cargoInventory) addBinary(name, root string) error {
	isNewBinary := validCargoName(name) && i.binaries[name] == nil
	validBinary := i.current != nil && isNewBinary
	if !validBinary {
		return fmt.Errorf("invalid cargo binary: %q", name)
	}
	pkg := *i.current
	if root != "" {
		path := filepath.Join(root, "bin", name)
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		pkg.Path = absolute
	}
	i.binaries[name], i.hasBins = &pkg, true
	return nil
}

func validCargoName(name string) bool {
	valid, _ := regexp.MatchString(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`, name)
	return valid
}

func validCargoVersion(version string) bool {
	valid, _ := regexp.MatchString(`^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)*$`, version)
	return valid
}

func resolveCargoPaths(binaries map[string]*core.PackageInfo) {
	if len(binaries) == 0 {
		return
	}
	for _, root := range cargoInstallRoots() {
		installed, err := readCargoExecutables(root)
		if err == nil {
			mergeCargoPaths(binaries, installed)
		}
	}
}

func mergeCargoPaths(binaries, installed map[string]*core.PackageInfo) {
	for name, pkg := range binaries {
		candidate := installed[name]
		canResolve := candidate != nil && pkg.Path == ""
		if !canResolve {
			continue
		}
		hasIdentity := pkg.Name == candidate.Name && pkg.Version == candidate.Version
		if hasIdentity {
			pkg.Path = candidate.Path
		}
	}
}

func cargoInstallRoots() []string {
	var roots []string
	paths := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range paths {
		isAbsoluteBin := filepath.IsAbs(dir) && filepath.Base(dir) == "bin"
		if isAbsoluteBin {
			roots = append(roots, filepath.Dir(dir))
		}
	}
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".cargo")
	}
	return existingCargoRoots(append(roots, home))
}

func existingCargoRoots(candidates []string) []string {
	var roots []string
	seen := make(map[string]bool)
	for _, root := range candidates {
		isInstalledRoot := !seen[root] && cargoRootHasReceipt(root)
		if isInstalledRoot {
			roots = append(roots, root)
			seen[root] = true
		}
	}
	return roots
}

func cargoRootHasReceipt(root string) bool {
	for _, name := range []string{".crates.toml", ".crates2.json"} {
		info, err := os.Stat(filepath.Join(root, name))
		hasReceipt := err == nil && info.Mode().IsRegular()
		if hasReceipt {
			return true
		}
	}
	return false
}
