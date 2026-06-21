package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

// Style and color constants
var (
	// Styles
	titleStyle = newStyle().
			Bold(true).
			Foreground(color("205"))

	subtitleStyle = newStyle().
			Foreground(color("241"))

	successStyle = newStyle().
			Foreground(color("42"))

	errorStyle = newStyle().
			Foreground(color("196"))

	infoStyle = newStyle().
			Foreground(color("86"))
)

const (
	defaultListLimit = 20
	defaultPageSize  = 12

	formatTable = "table"
	formatJSON  = "json"
	formatCSV   = "csv"

	homebrewCommandName = "brew"
	npmCommandName      = "npm"
	pnpmCommandName     = "pnpm"
	bunCommandName      = "bun"
	pipCommandName      = "pip"
	uvCommandName       = "uv"

	homebrewCaskTool = "homebrew-cask"
	homebrewCaskFlag = "--cask"
	npmGlobalFlag    = "-g"
	pipYesFlag       = "-y"

	configSubcommand    = "config"
	getSubcommand       = "get"
	npmPrefixConfigName = "prefix"
	uninstallSubcommand = "uninstall"
	removeSubcommand    = "remove"

	actionQuit      = "q"
	actionNext      = "n"
	actionPrevious  = "p"
	actionSearch    = "/"
	actionUninstall = "u"

	removeFilePlan               = "remove-file"
	packageNameAllowedCharacters = "@._+-/"
	packageIndexColumnWidth      = 3
	packageToolColumnWidth       = 14
	packageNameColumnWidth       = 34
	packageUsageColumnWidth      = 4
)

type executablePathDeps struct {
	getenv        func(string) string
	userHomeDir   func() (string, error)
	lookPath      func(string) (string, error)
	commandOutput func(string, ...string) ([]byte, error)
}

var defaultExecutablePathDeps = executablePathDeps{
	getenv:      os.Getenv,
	userHomeDir: os.UserHomeDir,
	lookPath:    exec.LookPath,
	commandOutput: func(name string, args ...string) ([]byte, error) {
		// #nosec G204 -- callers pass fixed command names and argument lists from allowlisted helpers.
		return exec.Command(name, args...).Output()
	},
}

// closeStore closes the storage and logs any errors
func closeStore(store storage.Storage) {
	if err := store.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close storage: %v\n", err)
	}
}

// isTerminal returns true if stdin is a terminal
func isTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// flagString is a helper to get string flag value
func flagString(cmd *command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

// flagInt is a helper to get int flag value
func flagInt(cmd *command, name string) int {
	value, _ := cmd.Flags().GetInt(name)
	return value
}

// flagBool is a helper to get bool flag value
func flagBool(cmd *command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

// parseDuration parses duration strings like "24h", "7d", "30d", "1w", "1m"
func parseDuration(s string) (time.Duration, error) {
	// Support formats like "24h", "7d", "30d"
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	if strings.HasSuffix(s, "w") {
		weeks, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil {
			return 0, err
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}

	if strings.HasSuffix(s, "m") && !strings.Contains(s, "h") {
		months, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err != nil {
			return 0, err
		}
		return time.Duration(months) * 30 * 24 * time.Hour, nil
	}

	return time.ParseDuration(s)
}

// getToolColor returns the ANSI color code for a tool
func getToolColor(tool string) color {
	switch core.NormalizeToolName(tool) {
	case "homebrew":
		return color("214") // Orange
	case "npm":
		return color("196") // Red
	case "pnpm":
		return color("208") // Orange
	case "bun":
		return color("230") // Cream
	case "go":
		return color("86") // Cyan
	case "pip", "python", "uv", "poetry":
		return color("226") // Yellow
	case "gem", "ruby":
		return color("160") // Red
	case "cargo", "rust":
		return color("208") // Orange
	default:
		return color("250") // Gray
	}
}

// formatLastUsed formats a timestamp for display
func formatLastUsed(lastUsed time.Time) string {
	if lastUsed.IsZero() {
		return "never"
	}
	return lastUsed.Format("2006-01-02")
}

// truncate truncates a string to maxLength, adding ellipsis if truncated
func truncate(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 1 {
		return value[:maxLength]
	}
	return value[:maxLength-1] + "."
}

// shouldSkipExecutableWrapper returns true if the executable should not be wrapped
func shouldSkipExecutableWrapper(name string) bool {
	switch name {
	case "", ".", "..", "diu", "brew", core.ToolNPM, core.ToolPNPM, core.ToolBun, core.ToolGo, core.ToolPip, "pip3", core.ToolUV, core.ToolPoetry:
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

// packageNameForExecutable extracts package name from executable path
func packageNameForExecutable(tool, path, name string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	slashPath := filepath.ToSlash(resolved)

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

// pathSegmentAfter returns the first segment after marker in path
func pathSegmentAfter(path, marker string) string {
	parts := strings.SplitN(path, marker, 2)
	if len(parts) != 2 {
		return ""
	}
	segments := strings.Split(parts[1], "/")
	if len(segments) == 0 {
		return ""
	}
	return segments[0]
}

// npmPackageFromPath extracts package name from npm module path
func npmPackageFromPath(path string) string {
	parts := strings.SplitN(path, "/node_modules/", 2)
	if len(parts) != 2 {
		return ""
	}
	segments := strings.Split(parts[1], "/")
	if len(segments) == 0 {
		return ""
	}
	if strings.HasPrefix(segments[0], "@") && len(segments) > 1 {
		return segments[0] + "/" + segments[1]
	}
	return segments[0]
}

// npmGlobalBinDir returns the npm global bin directory
func npmGlobalBinDir() string {
	if _, err := exec.LookPath(npmCommandName); err != nil {
		return ""
	}
	// #nosec G204 -- npm command and arguments are fixed constants used only to locate the global bin directory.
	output, err := exec.Command(npmCommandName, configSubcommand, getSubcommand, npmPrefixConfigName).Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(output))
	if prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "bin")
}

// pnpmGlobalBinDir returns the pnpm global executable directory.
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

// bunGlobalBinDir returns the Bun global executable directory.
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

// pythonUserBaseBinDir returns the Python user-base script directory.
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

// uvToolBinDir returns the uv tool executable directory.
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
		if _, err := deps.lookPath(name); err == nil {
			return name, nil
		} else {
			lastErr = err
		}
	}
	return "", lastErr
}

// goBinaryDir returns the Go binary directory
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

// validatePackageManagerName validates a package manager package name
func validatePackageManagerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("package name cannot contain leading or trailing whitespace")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("package name cannot start with a flag prefix: %s", name)
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("package name cannot be an absolute or incomplete path: %s", name)
	}
	if strings.Contains(name, "..") || strings.Contains(name, "//") {
		return fmt.Errorf("package name contains an unsafe path segment: %s", name)
	}

	hasAlnum := false
	for _, char := range name {
		if char >= 'a' && char <= 'z' {
			hasAlnum = true
			continue
		}
		if char >= 'A' && char <= 'Z' {
			hasAlnum = true
			continue
		}
		if char >= '0' && char <= '9' {
			hasAlnum = true
			continue
		}
		if strings.ContainsRune(packageNameAllowedCharacters, char) {
			continue
		}
		return fmt.Errorf("package name contains unsupported character %q", char)
	}
	if !hasAlnum {
		return fmt.Errorf("package name must contain a letter or number")
	}
	return nil
}

// validateRemovableExecutablePath validates a path for removal as an executable
func validateRemovableExecutablePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return "", fmt.Errorf("executable path contains an unsafe path segment: %s", path)
		}
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", cleanPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("refusing to remove directory: %s", cleanPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return "", fmt.Errorf("refusing to remove non-executable file: %s", cleanPath)
	}

	return cleanPath, nil
}

// validateExecutablePath validates a path as an executable
func validateExecutablePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
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

// wrapperNameForPackage returns the wrapper name for a package
func wrapperNameForPackage(pkg *core.PackageInfo) string {
	if pkg.Path != "" {
		return filepath.Base(pkg.Path)
	}
	return pkg.Name
}

// executableWrapperPath returns the path for an executable wrapper
func executableWrapperPath(wrapperDir, name string) (string, error) {
	if strings.TrimSpace(wrapperDir) == "" {
		return "", fmt.Errorf("wrapper directory cannot be empty")
	}
	if shouldSkipExecutableWrapper(name) || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid wrapper name: %s", name)
	}

	cleanDir := filepath.Clean(wrapperDir)
	wrapperPath := filepath.Join(cleanDir, name)
	relativePath, err := filepath.Rel(cleanDir, wrapperPath)
	if err != nil {
		return "", fmt.Errorf("failed to validate wrapper path: %w", err)
	}
	if relativePath == "." || strings.HasPrefix(relativePath, "..") {
		return "", fmt.Errorf("wrapper path escapes wrapper directory: %s", wrapperPath)
	}

	return wrapperPath, nil
}

// writeOwnerExecutableFile writes data to a file with executable permissions
func writeOwnerExecutableFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to create executable file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close executable file: %w", closeErr)
		}
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write executable file: %w", err)
	}
	if err := file.Chmod(core.OwnerExecutableMode); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}
	return nil
}

// readPrompt reads a line from the reader with the given prompt
func readPrompt(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

// newMonitor creates a monitor for the given tool
func newMonitor(tool string) (monitors.Monitor, error) {
	switch core.NormalizeToolName(tool) {
	case core.ToolHomebrew:
		return monitors.NewHomebrewMonitor(), nil
	case core.ToolNPM:
		return monitors.NewNPMMonitor(), nil
	case core.ToolPNPM:
		return monitors.NewPNPMMonitor(), nil
	case core.ToolBun:
		return monitors.NewBunMonitor(), nil
	case core.ToolGo:
		return monitors.NewGoMonitor(), nil
	case core.ToolPip:
		return monitors.NewPipMonitor(), nil
	case core.ToolUV:
		return monitors.NewUVMonitor(), nil
	case core.ToolPoetry:
		return monitors.NewPoetryMonitor(), nil
	default:
		return nil, fmt.Errorf("unsupported tool: %s", tool)
	}
}

// enrichExecutionRecord enriches an execution record with parsed metadata
func enrichExecutionRecord(config *core.Config, record *core.ExecutionRecord) {
	record.Tool = core.NormalizeToolName(record.Tool)
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}

	monitor, err := newMonitor(record.Tool)
	if err != nil {
		return
	}

	parseConfig := *config
	parseConfig.Monitoring.Process.AutoInstallWrappers = false
	if err := monitor.Initialize(&parseConfig); err != nil {
		return
	}

	monitors.EnrichExecutionRecord(monitor, record)
}

// supportsUninstall returns true if the package tool supports uninstall
func supportsUninstall(pkg *core.PackageInfo) bool {
	switch pkg.Tool {
	case core.ToolHomebrew, homebrewCaskTool, core.ToolNPM, core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV, core.ToolGo, core.ToolGoBinary:
		return true
	default:
		return false
	}
}

// runPreparedCommand runs a prepared exec.Cmd
func runPreparedCommand(command *exec.Cmd) error {
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		return fmt.Errorf("uninstall failed: %w", err)
	}
	return nil
}

// runHomebrewUninstall runs brew uninstall for a package
func runHomebrewUninstall(name string, cask bool) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	var command *exec.Cmd
	if cask {
		// #nosec G204 -- command is allowlisted and package name is validated before execution.
		command = exec.Command(homebrewCommandName, uninstallSubcommand, homebrewCaskFlag, name)
	} else {
		// #nosec G204 -- command is allowlisted and package name is validated before execution.
		command = exec.Command(homebrewCommandName, uninstallSubcommand, name)
	}
	return runPreparedCommand(command)
}

// runNPMUninstall runs npm uninstall for a package
func runNPMUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(npmCommandName, uninstallSubcommand, npmGlobalFlag, name)
	return runPreparedCommand(command)
}

// runPNPMUninstall runs pnpm remove -g for a package.
func runPNPMUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(pnpmCommandName, removeSubcommand, npmGlobalFlag, name)
	return runPreparedCommand(command)
}

// runBunUninstall runs bun remove -g for a package.
func runBunUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(bunCommandName, removeSubcommand, npmGlobalFlag, name)
	return runPreparedCommand(command)
}

// runPipUninstall runs pip uninstall -y for a package.
func runPipUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	commandName, err := firstExistingCommand(pipCommandName, "pip3")
	if err != nil {
		return fmt.Errorf("pip not found: %w", err)
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(commandName, uninstallSubcommand, pipYesFlag, name)
	return runPreparedCommand(command)
}

// runUVUninstall runs uv tool uninstall for a package.
func runUVUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(uvCommandName, "tool", uninstallSubcommand, name)
	return runPreparedCommand(command)
}

// removeGoBinary removes a Go binary
func removeGoBinary(pkg *core.PackageInfo) error {
	binaryPath, err := validateRemovableExecutablePath(pkg.Path)
	if err != nil {
		return err
	}
	if err := os.Remove(binaryPath); err != nil {
		return fmt.Errorf("failed to remove %s: %w", binaryPath, err)
	}
	return nil
}

// uninstallPlan returns the command plan for uninstalling a package
func uninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	switch pkg.Tool {
	case core.ToolHomebrew:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{homebrewCommandName, uninstallSubcommand, pkg.Name}, nil
	case homebrewCaskTool:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{homebrewCommandName, uninstallSubcommand, homebrewCaskFlag, pkg.Name}, nil
	case core.ToolNPM:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{npmCommandName, uninstallSubcommand, npmGlobalFlag, pkg.Name}, nil
	case core.ToolPNPM:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{pnpmCommandName, removeSubcommand, npmGlobalFlag, pkg.Name}, nil
	case core.ToolBun:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{bunCommandName, removeSubcommand, npmGlobalFlag, pkg.Name}, nil
	case core.ToolPip:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{pipCommandName, uninstallSubcommand, pipYesFlag, pkg.Name}, nil
	case core.ToolUV:
		if err := validatePackageManagerName(pkg.Name); err != nil {
			return nil, err
		}
		return []string{uvCommandName, "tool", uninstallSubcommand, pkg.Name}, nil
	case core.ToolGo, core.ToolGoBinary:
		if pkg.Path == "" {
			return nil, fmt.Errorf("go package %s has no executable path to remove", pkg.Name)
		}
		return []string{removeFilePlan}, nil
	default:
		return nil, fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

// printableUninstallPlan returns a human-readable uninstall plan
func printableUninstallPlan(pkg *core.PackageInfo, plan []string) []string {
	if len(plan) == 1 && plan[0] == removeFilePlan {
		return []string{"rm", pkg.Path}
	}
	return plan
}

// packageMatchesSearch returns true if the package matches the search query
func packageMatchesSearch(pkg *core.PackageInfo, search string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		pkg.Name,
		pkg.Tool,
		pkg.Version,
		pkg.Path,
	}, " "))
	return strings.Contains(haystack, search)
}

// packageUnusedSince returns true if the package hasn't been used since cutoff
func packageUnusedSince(pkg *core.PackageInfo, cutoff time.Time) bool {
	return pkg.LastUsed.IsZero() || pkg.LastUsed.Before(cutoff)
}

// sortPackages sorts packages by usage count (descending), last used (descending), tool, then name
func sortPackages(packages []*core.PackageInfo) {
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].UsageCount != packages[j].UsageCount {
			return packages[i].UsageCount > packages[j].UsageCount
		}
		if !packages[i].LastUsed.Equal(packages[j].LastUsed) {
			return packages[i].LastUsed.After(packages[j].LastUsed)
		}
		if packages[i].Tool != packages[j].Tool {
			return packages[i].Tool < packages[j].Tool
		}
		return packages[i].Name < packages[j].Name
	})
}
