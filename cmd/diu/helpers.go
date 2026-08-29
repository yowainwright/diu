package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
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
	pip3CommandName     = "pip3"
	uvCommandName       = "uv"

	homebrewCaskTool = core.ToolHomebrewCask
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
	commandTimeout               = 2 * time.Minute
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

func cliOutput() *dx.Out {
	return dx.TerminalOut()
}

// closeStore closes the storage and logs any errors
func closeStore(store storage.Storage) {
	if err := store.Close(); err != nil {
		cliOutput().Status(dx.Error, fmt.Sprintf("failed to close storage: %v", err))
	}
}

func closeStoreDuringActivity(store storage.Storage, activity *dx.Activity) {
	if err := store.Close(); err != nil {
		activity.Notice(dx.Error, fmt.Sprintf("failed to close storage: %v", err))
	}
}

// isTerminal returns true if stdin is a terminal
func isTerminal() bool {
	return cliOutput().CanPrompt()
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

// parseDuration parses duration strings like "24h", "7d", "30d", "1w", "1mo"
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		return parseDurationUnit(s, "d", 24*time.Hour)
	}

	if strings.HasSuffix(s, "w") {
		week := 7 * 24 * time.Hour
		return parseDurationUnit(s, "w", week)
	}

	if strings.HasSuffix(s, "mo") {
		month := 30 * 24 * time.Hour
		return parseDurationUnit(s, "mo", month)
	}

	return time.ParseDuration(s)
}

func parseDurationUnit(s, suffix string, unit time.Duration) (time.Duration, error) {
	count, err := strconv.Atoi(strings.TrimSuffix(s, suffix))
	if err != nil {
		return 0, err
	}
	duration := time.Duration(count) * unit
	return duration, nil
}

// formatLastUsed formats a timestamp for display
func formatLastUsed(lastUsed time.Time) string {
	if lastUsed.IsZero() {
		return "never"
	}
	return lastUsed.Format("2006-01-02")
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
	hasScope := strings.HasPrefix(segments[0], "@")
	hasName := len(segments) > 1
	isScopedPackage := hasScope && hasName
	if isScopedPackage {
		scopedPackage := segments[0] + "/" + segments[1]
		return scopedPackage
	}
	return segments[0]
}

// npmGlobalBinDir returns the npm global bin directory
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
		_, err := deps.lookPath(name)
		if err == nil {
			return name, nil
		}
		lastErr = err
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
	if err := validatePackageNameShape(name); err != nil {
		return err
	}
	return validatePackageNameCharacters(name)
}

func validatePackageNameShape(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("package name cannot contain leading or trailing whitespace")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("package name cannot start with a flag prefix: %s", name)
	}
	absoluteOrIncomplete := strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/")
	if absoluteOrIncomplete {
		return fmt.Errorf("package name cannot be an absolute or incomplete path: %s", name)
	}
	unsafePath := strings.Contains(name, "..") || strings.Contains(name, "//")
	if unsafePath {
		return fmt.Errorf("package name contains an unsafe path segment: %s", name)
	}
	return nil
}

func validatePackageNameCharacters(name string) error {
	hasAlnum := false
	for _, char := range name {
		if packageNameAlphaNumeric(char) {
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

func packageNameAlphaNumeric(char rune) bool {
	isLower := char >= 'a' && char <= 'z'
	isUpper := char >= 'A' && char <= 'Z'
	isDigit := char >= '0' && char <= '9'
	isAlnum := isLower || isUpper || isDigit
	return isAlnum
}

// validateRemovableExecutablePath validates a path for removal as an executable
func validateRemovableExecutablePath(path string) (string, error) {
	if err := rejectEmptyExecutablePath(path); err != nil {
		return "", err
	}
	if err := rejectParentPathSegments(path); err != nil {
		return "", err
	}
	cleanPath, err := cleanAbsoluteExecutablePath(path)
	if err != nil {
		return "", err
	}
	if err := validateRemovableExecutableFile(cleanPath); err != nil {
		return "", err
	}
	return cleanPath, nil
}

func validateRemovableExecutableFile(cleanPath string) error {
	info, err := os.Stat(cleanPath)
	if err != nil {
		return fmt.Errorf("failed to inspect executable %s: %w", cleanPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to remove directory: %s", cleanPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return fmt.Errorf("refusing to remove non-executable file: %s", cleanPath)
	}
	return nil
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

// validateExecutablePath validates a path as an executable
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
	if err := validateWrapperName(name); err != nil {
		return "", err
	}

	cleanDir := filepath.Clean(wrapperDir)
	wrapperPath := filepath.Join(cleanDir, name)
	relativePath, err := filepath.Rel(cleanDir, wrapperPath)
	if err != nil {
		return "", fmt.Errorf("failed to validate wrapper path: %w", err)
	}
	if wrapperEscapesDirectory(relativePath) {
		return "", fmt.Errorf("wrapper path escapes wrapper directory: %s", wrapperPath)
	}

	return wrapperPath, nil
}

func validateWrapperName(name string) error {
	if shouldSkipExecutableWrapper(name) {
		return fmt.Errorf("invalid wrapper name: %s", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid wrapper name: %s", name)
	}
	return nil
}

func wrapperEscapesDirectory(relativePath string) bool {
	if relativePath == "." {
		return true
	}
	return strings.HasPrefix(relativePath, "..")
}

// writeOwnerExecutableFile writes data to a file with executable permissions
func writeOwnerExecutableFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to create executable file: %w", err)
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "failed to close executable file")
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write executable file: %w", err)
	}
	if err := file.Chmod(core.OwnerExecutableMode); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}
	return nil
}

// newMonitor creates a monitor for the given tool
func newMonitor(tool string) (monitors.Monitor, error) {
	normalizedTool := core.NormalizeToolName(tool)
	switch normalizedTool {
	case core.ToolHomebrew:
		return monitors.NewHomebrewMonitor(), nil
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		return newJavaScriptMonitor(normalizedTool)
	case core.ToolGo:
		return monitors.NewGoMonitor(), nil
	case core.ToolPip, core.ToolUV, core.ToolPoetry:
		return newPythonMonitor(normalizedTool)
	default:
		return nil, fmt.Errorf("unsupported tool: %s", tool)
	}
}

func newJavaScriptMonitor(tool string) (monitors.Monitor, error) {
	switch tool {
	case core.ToolNPM:
		return monitors.NewNPMMonitor(), nil
	case core.ToolPNPM:
		return monitors.NewPNPMMonitor(), nil
	default:
		return monitors.NewBunMonitor(), nil
	}
}

func newPythonMonitor(tool string) (monitors.Monitor, error) {
	switch tool {
	case core.ToolPip:
		return monitors.NewPipMonitor(), nil
	case core.ToolUV:
		return monitors.NewUVMonitor(), nil
	default:
		return monitors.NewPoetryMonitor(), nil
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

func runCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out := cliOutput()
	runner := dx.NewRunner(out.Stdin(), out.Stderr(), out.Stderr())
	if err := runner.Run(ctx, name, args...); err != nil {
		return fmt.Errorf("uninstall failed: %w", err)
	}
	return nil
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

// runHomebrewUninstall runs brew uninstall for a package
func runHomebrewUninstall(name string, cask bool) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	if cask {
		return runCommand(homebrewCommandName, uninstallSubcommand, homebrewCaskFlag, name)
	}
	return runCommand(homebrewCommandName, uninstallSubcommand, name)
}

// runNPMUninstall runs npm uninstall for a package
func runNPMUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	return runCommand(npmCommandName, uninstallSubcommand, npmGlobalFlag, name)
}

// runPNPMUninstall runs pnpm remove -g for a package.
func runPNPMUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	return runCommand(pnpmCommandName, removeSubcommand, npmGlobalFlag, name)
}

// runBunUninstall runs bun remove -g for a package.
func runBunUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	return runCommand(bunCommandName, removeSubcommand, npmGlobalFlag, name)
}

// runPipUninstall runs pip uninstall -y for a package.
func runPipUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	commandName, err := pipCommandForUninstall()
	if err != nil {
		return fmt.Errorf("pip not found: %w", err)
	}

	args, err := pipUninstallArgs(commandName, name)
	if err != nil {
		return err
	}
	return runCommand(commandName, args...)
}

func pipCommandForUninstall() (string, error) {
	return firstExistingCommand(pip3CommandName, pipCommandName)
}

func pipUninstallArgs(commandName, name string) ([]string, error) {
	switch commandName {
	case pip3CommandName:
		return []string{uninstallSubcommand, pipYesFlag, name}, nil
	case pipCommandName:
		return []string{uninstallSubcommand, pipYesFlag, name}, nil
	default:
		return nil, fmt.Errorf("unsupported pip command: %s", commandName)
	}
}

// runUVUninstall runs uv tool uninstall for a package.
func runUVUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	return runCommand(uvCommandName, "tool", uninstallSubcommand, name)
}

// removeGoBinary removes a Go binary
func removeGoBinary(pkg *core.PackageInfo) error {
	binaryPath, err := validateRemovableExecutablePath(pkg.Path)
	if err != nil {
		return err
	}
	if pkg.Fingerprint == "" {
		return fmt.Errorf("go binary %s has no scan fingerprint; run diu scan before uninstalling", pkg.Name)
	}
	currentFingerprint, err := safefs.SHA256(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to fingerprint %s: %w", binaryPath, err)
	}
	if currentFingerprint != pkg.Fingerprint {
		return fmt.Errorf("go binary %s changed since the last scan; refusing to remove %s", pkg.Name, binaryPath)
	}
	return removeGoBinaryPath(binaryPath)
}

func removeGoBinaryPath(binaryPath string) error {
	if err := os.Remove(binaryPath); err != nil {
		return fmt.Errorf("failed to remove %s: %w", binaryPath, err)
	}
	return nil
}

// uninstallPlan returns the command plan for uninstalling a package
func uninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	switch pkg.Tool {
	case core.ToolHomebrew, homebrewCaskTool:
		return homebrewUninstallPlan(pkg)
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		return javascriptUninstallPlan(pkg)
	case core.ToolPip:
		return pipUninstallPlan(pkg)
	case core.ToolUV:
		return namedPackageUninstallPlan(pkg, uvCommandName, "tool", uninstallSubcommand)
	case core.ToolGo, core.ToolGoBinary:
		return goBinaryUninstallPlan(pkg)
	default:
		return nil, fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

func homebrewUninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	if pkg.Tool == homebrewCaskTool {
		return namedPackageUninstallPlan(pkg, homebrewCommandName, uninstallSubcommand, homebrewCaskFlag)
	}
	return namedPackageUninstallPlan(pkg, homebrewCommandName, uninstallSubcommand)
}

func javascriptUninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	switch pkg.Tool {
	case core.ToolNPM:
		return namedPackageUninstallPlan(pkg, npmCommandName, uninstallSubcommand, npmGlobalFlag)
	case core.ToolPNPM:
		return namedPackageUninstallPlan(pkg, pnpmCommandName, removeSubcommand, npmGlobalFlag)
	default:
		return namedPackageUninstallPlan(pkg, bunCommandName, removeSubcommand, npmGlobalFlag)
	}
}

func namedPackageUninstallPlan(pkg *core.PackageInfo, commandName string, args ...string) ([]string, error) {
	if err := validatePackageManagerName(pkg.Name); err != nil {
		return nil, err
	}
	plan := append([]string{commandName}, args...)
	return append(plan, pkg.Name), nil
}

func pipUninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	if err := validatePackageManagerName(pkg.Name); err != nil {
		return nil, err
	}
	commandName, err := pipCommandForUninstall()
	if err != nil {
		return nil, fmt.Errorf("pip not found: %w", err)
	}
	return []string{commandName, uninstallSubcommand, pipYesFlag, pkg.Name}, nil
}

func goBinaryUninstallPlan(pkg *core.PackageInfo) ([]string, error) {
	if pkg.Path == "" {
		return nil, fmt.Errorf("go package %s has no executable path to remove", pkg.Name)
	}
	return []string{removeFilePlan}, nil
}

// printableUninstallPlan returns a human-readable uninstall plan
func printableUninstallPlan(pkg *core.PackageInfo, plan []string) []string {
	if len(plan) != 1 {
		return plan
	}
	if plan[0] != removeFilePlan {
		return plan
	}
	return []string{"rm", pkg.Path}
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
	slices.SortFunc(packages, func(a, b *core.PackageInfo) int {
		if order := cmp.Compare(b.UsageCount, a.UsageCount); order != 0 {
			return order
		}
		if order := b.LastUsed.Compare(a.LastUsed); order != 0 {
			return order
		}
		if order := strings.Compare(a.Tool, b.Tool); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
}
