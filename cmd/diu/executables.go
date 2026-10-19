package main

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/safefs"
)

type executableTarget struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

func currentShellHomeDir() (string, error) {
	activeHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(activeHome) {
		return "", fmt.Errorf("home directory must be absolute: %s", activeHome)
	}
	return filepath.Clean(activeHome), nil
}

func discoverExecutables(config *core.Config) []executableTarget {
	targets := make(map[string]executableTarget)
	addToolExecutableDirs(config, targets, core.ToolHomebrew, nil)
	addToolExecutableDirs(config, targets, core.ToolNPM, npmGlobalBinDir)
	addToolExecutableDirs(config, targets, core.ToolPNPM, pnpmGlobalBinDir)
	addToolExecutableDirs(config, targets, core.ToolBun, bunGlobalBinDir)
	addGoExecutableDirs(config, targets)
	addToolExecutableDirs(config, targets, core.ToolPip, pythonUserBaseBinDir)
	addToolExecutableDirs(config, targets, core.ToolUV, uvToolBinDir)
	addCargoExecutables(config, targets)
	addPipxExecutables(config, targets)
	return slices.SortedFunc(maps.Values(targets), compareExecutableTargets)
}

func addCargoExecutables(config *core.Config, targets map[string]executableTarget) {
	if !monitoringToolEnabled(config, core.ToolCargo) {
		return
	}
	binaries, _ := monitors.CargoExecutables("")
	for name, pkg := range binaries {
		addOwnedExecutable(targets, name, pkg)
	}
}

func addPipxExecutables(config *core.Config, targets map[string]executableTarget) {
	if !monitoringToolEnabled(config, core.ToolPipx) {
		return
	}
	binaries, _ := monitors.PipxExecutables()
	for name, pkg := range binaries {
		addOwnedExecutable(targets, name, pkg)
	}
}

func addOwnedExecutable(targets map[string]executableTarget, name string, pkg *core.PackageInfo) {
	canTrack := !shouldSkipExecutable(name) && usableExecutablePath(pkg.Path)
	if !canTrack {
		return
	}
	active, err := exec.LookPath(name)
	isActive := err == nil && sameExecutable(active, pkg.Path)
	if !isActive {
		return
	}
	existing := targets[name]
	samePath := sameExecutable(existing.OriginalPath, pkg.Path)
	keepExisting := !samePath && preferExistingExecutable(existing, pkg.Path)
	if keepExisting {
		return
	}
	targets[name] = executableTarget{Name: name, OriginalPath: pkg.Path, Tool: pkg.Tool, Package: pkg.Name}
}

func compareExecutableTargets(a, b executableTarget) int {
	return strings.Compare(a.Name, b.Name)
}

func monitoringToolEnabled(config *core.Config, tool string) bool {
	for _, enabled := range config.Monitoring.EnabledTools {
		if core.NormalizeToolName(enabled) == tool {
			return true
		}
	}
	return false
}

func executableDirs(primary string, extra []string) []string {
	if primary == "" {
		return extra
	}
	return append([]string{primary}, extra...)
}

func addToolExecutableDirs(config *core.Config, targets map[string]executableTarget, tool string, primaryDir func() string) {
	if !monitoringToolEnabled(config, tool) {
		return
	}
	watchPaths := config.Monitoring.Filesystem.WatchPaths
	dirs := watchPaths[tool]
	if primaryDir != nil {
		dirs = executableDirs(primaryDir(), dirs)
	}
	for _, dir := range dirs {
		addExecutableDir(targets, tool, dir)
	}
}

func addGoExecutableDirs(config *core.Config, targets map[string]executableTarget) {
	if monitoringToolEnabled(config, core.ToolGo) {
		addExecutableDir(targets, core.ToolGoBinary, goBinaryDir(config))
	}
}

func addExecutableDir(targets map[string]executableTarget, tool, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		addExecutableEntry(targets, tool, dir, entry.Name())
	}
}

func addExecutableEntry(targets map[string]executableTarget, tool, dir, name string) {
	if shouldSkipExecutable(name) {
		return
	}
	path := filepath.Join(dir, name)
	if !usableExecutablePath(path) {
		return
	}
	pkg := packageNameForExecutable(tool, path, name)
	if pkg == "" {
		return
	}
	if preferExistingExecutable(targets[name], path) {
		return
	}
	targets[name] = executableTarget{Name: name, OriginalPath: path, Tool: tool, Package: pkg}
}

func preferExistingExecutable(existing executableTarget, path string) bool {
	if existing.OriginalPath == "" {
		return false
	}
	return executablePathPriority(existing.OriginalPath) <= executablePathPriority(path)
}

func executablePathPriority(path string) int {
	paths := filepath.SplitList(os.Getenv("PATH"))
	for index, dir := range paths {
		if sameExecutable(filepath.Join(dir, filepath.Base(path)), path) {
			return index
		}
	}
	return len(paths)
}

func usableExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	return info.Mode()&core.ExecutableModeMask != 0
}

const (
	configSubcommand    = "config"
	getSubcommand       = "get"
	npmPrefixConfigName = "prefix"
)

type executablePathDeps struct {
	getenv        func(string) string
	userHomeDir   func() (string, error)
	lookPath      func(string) (string, error)
	commandOutput func(string, ...string) ([]byte, error)
}

var defaultExecutablePathDeps = executablePathDeps{
	getenv:        os.Getenv,
	userHomeDir:   os.UserHomeDir,
	lookPath:      exec.LookPath,
	commandOutput: runCommandOutput,
}

func shouldSkipExecutable(name string) bool {
	switch name {
	case "", ".", "..", "diu", "brew", core.ToolNPM, core.ToolPNPM, core.ToolBun, core.ToolGo, core.ToolCargo, core.ToolPipx, core.ToolPip, "pip3", core.ToolUV, core.ToolPoetry:
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func packageNameForExecutable(tool, path, name string) string {
	slashPath := resolvedExecutablePath(path)
	if !executableMatchesManager(tool, slashPath) {
		return ""
	}

	switch tool {
	case core.ToolHomebrew:
		if pkg := pathSegmentAfter(slashPath, "/Cellar/"); pkg != "" {
			return pkg
		}
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		if pkg := npmPackageFromPath(slashPath); pkg != "" {
			return pkg
		}
	}

	return name
}

func resolvedExecutablePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	return filepath.ToSlash(resolved)
}

func executableMatchesManager(tool, resolvedPath string) bool {
	jsPackage := npmPackageFromPath(resolvedPath)
	brewPackage := pathSegmentAfter(resolvedPath, "/Cellar/")
	switch tool {
	case core.ToolHomebrew:
		matches := brewPackage != "" || jsPackage == ""
		return matches
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		return brewPackage == ""
	default:
		return true
	}
}

func pathSegmentAfter(path, marker string) string {
	_, rest, found := strings.Cut(path, marker)
	if !found {
		return ""
	}
	segment, _, _ := strings.Cut(rest, "/")
	return segment
}

func npmPackageFromPath(path string) string {
	parts := strings.SplitN(path, "/node_modules/", 2)
	if len(parts) != 2 {
		return ""
	}
	segments := strings.Split(parts[1], "/")
	if len(segments) == 0 {
		return ""
	}
	hasScope := strings.HasPrefix(segments[0], "@")
	hasName := len(segments) > 1
	isScopedPackage := hasScope && hasName
	if isScopedPackage {
		scopedPackage := segments[0] + "/" + segments[1]
		return scopedPackage
	}
	return segments[0]
}

func npmGlobalBinDir() string {
	if _, err := exec.LookPath(npmCommandName); err != nil {
		return ""
	}
	output, err := runCommandOutput(npmCommandName, configSubcommand, getSubcommand, npmPrefixConfigName)
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(output))
	if prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "bin")
}

func pnpmGlobalBinDir() string {
	return pnpmGlobalBinDirWithDeps(defaultExecutablePathDeps)
}

func pnpmGlobalBinDirWithDeps(deps executablePathDeps) string {
	if pnpmHome := deps.getenv("PNPM_HOME"); pnpmHome != "" {
		return pnpmHome
	}
	if _, err := deps.lookPath(pnpmCommandName); err != nil {
		return ""
	}
	output, err := deps.commandOutput(pnpmCommandName, "bin", npmGlobalFlag)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func bunGlobalBinDir() string {
	return bunGlobalBinDirWithDeps(defaultExecutablePathDeps)
}

func bunGlobalBinDirWithDeps(deps executablePathDeps) string {
	if bunInstall := deps.getenv("BUN_INSTALL"); bunInstall != "" {
		return filepath.Join(bunInstall, "bin")
	}
	homeDir := deps.getenv("HOME")
	if homeDir == "" {
		var err error
		homeDir, err = deps.userHomeDir()
		if err != nil {
			return ""
		}
	}
	return filepath.Join(homeDir, ".bun", "bin")
}

func pythonUserBaseBinDir() string {
	return pythonUserBaseBinDirWithDeps(defaultExecutablePathDeps)
}

func pythonUserBaseBinDirWithDeps(deps executablePathDeps) string {
	python, err := firstExistingCommandWithDeps(deps, "python3", "python")
	if err != nil {
		return ""
	}
	output, err := deps.commandOutput(python, "-m", "site", "--user-base")
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(output))
	if base == "" {
		return ""
	}
	return filepath.Join(base, "bin")
}

func uvToolBinDir() string {
	return uvToolBinDirWithDeps(defaultExecutablePathDeps)
}

func uvToolBinDirWithDeps(deps executablePathDeps) string {
	if dir := deps.getenv("UV_TOOL_BIN_DIR"); dir != "" {
		return dir
	}
	homeDir := deps.getenv("HOME")
	if homeDir == "" {
		var err error
		homeDir, err = deps.userHomeDir()
		if err != nil {
			return ""
		}
	}
	return filepath.Join(homeDir, ".local", "bin")
}

func firstExistingCommand(names ...string) (string, error) {
	return firstExistingCommandWithDeps(defaultExecutablePathDeps, names...)
}

func firstExistingCommandWithDeps(deps executablePathDeps, names ...string) (string, error) {
	var lastErr error
	for _, name := range names {
		_, err := deps.lookPath(name)
		if err == nil {
			return name, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func goBinaryDir(config *core.Config) string {
	return goBinaryDirWithDeps(config, defaultExecutablePathDeps)
}

func goBinaryDirWithDeps(config *core.Config, deps executablePathDeps) string {
	if config.Tools.Go.GoBin != "" {
		return config.Tools.Go.GoBin
	}
	if goBin := deps.getenv("GOBIN"); goBin != "" {
		return goBin
	}
	goPath := config.Tools.Go.GoPath
	if goPath == "" {
		goPath = deps.getenv("GOPATH")
	}
	if goPath == "" {
		homeDir, err := deps.userHomeDir()
		if err != nil {
			return ""
		}
		goPath = filepath.Join(homeDir, "go")
	}
	return filepath.Join(goPath, "bin")
}

func rejectEmptyExecutablePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("executable path cannot be empty")
	}
	return nil
}

func rejectParentPathSegments(path string) error {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return fmt.Errorf("executable path contains an unsafe path segment: %s", path)
		}
	}
	return nil
}

func cleanAbsoluteExecutablePath(path string) (string, error) {
	if err := rejectEmptyExecutablePath(path); err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
	}
	return cleanPath, nil
}

func validateExecutablePath(path string) (string, error) {
	cleanPath, err := cleanAbsoluteExecutablePath(path)
	if err != nil {
		return "", err
	}
	info, err := safefs.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", cleanPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("executable path is a directory: %s", cleanPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return "", fmt.Errorf("executable path is not executable: %s", cleanPath)
	}

	return cleanPath, nil
}

func runCommandOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := dx.NewRunner(nil, &stdout, &stderr)
	if err := runner.Run(ctx, name, args...); err != nil {
		return nil, commandOutputError(stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

func commandOutputError(stderr string, err error) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		return err
	}
	return fmt.Errorf("%s: %w", message, err)
}
