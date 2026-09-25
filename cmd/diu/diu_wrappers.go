package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/safefs"
)

type executableWrapper struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

type uninstallPaths struct {
	config          *core.Config
	homeDirs        []string
	wrapperDir      string
	shellWrapperDir string
}

const executableWrapperScriptTemplate = `#!/bin/bash
%s
DIU_SOCKET="%s"
DIU_BINARY="%s"
ORIGINAL_BINARY="%s"
DIU_TOOL="%s"
DIU_PACKAGE="%s"
DIU_EXECUTABLE="%s"
DIU_COMMAND="$DIU_EXECUTABLE"
DIU_ORIGINAL="$ORIGINAL_BINARY"
DIU_RECORD_COMMAND="$DIU_EXECUTABLE"
%s
%s
`

const executableWrapperPayload = `    "packages_affected": ["$(json_escape "$DIU_PACKAGE")"],
    "metadata": {
        "executable": "$(json_escape "$DIU_EXECUTABLE")",
        "original_path": "$(json_escape "$ORIGINAL_BINARY")"
    }
`

func removeSetupArtifacts(paths uninstallPaths, stopRecorder func() error) error {
	configErr := disableWrapperInstallation(paths.config)
	stopErr := stopRecorder()
	wrapperErr := removeGeneratedWrappers(paths.wrapperDir)
	shellErr := removeShellPathEntriesFromHomes(paths.homeDirs, paths.shellWrapperDir)
	return errors.Join(configErr, stopErr, wrapperErr, shellErr)
}

func disableWrapperInstallation(config *core.Config) error {
	if !config.Monitoring.Process.ShouldAutoInstallWrappers {
		return nil
	}
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	if err := config.SaveExisting(); err != nil {
		return fmt.Errorf("failed to disable automatic wrapper installation: %w", err)
	}
	return nil
}

func loadUninstallPaths() (uninstallPaths, error) {
	config, err := core.LoadConfig("")
	if err != nil {
		return uninstallPaths{}, fmt.Errorf("failed to load config: %w", err)
	}
	homeDirs, err := currentShellHomeDirs()
	if err != nil {
		return uninstallPaths{}, fmt.Errorf("failed to find home directory: %w", err)
	}
	shellWrapperDir := config.Monitoring.Process.WrapperDir
	wrapperDir, err := validateWrapperDir(shellWrapperDir, homeDirs)
	if err != nil {
		return uninstallPaths{}, err
	}
	return uninstallPaths{config: config, homeDirs: homeDirs, wrapperDir: wrapperDir, shellWrapperDir: shellWrapperDir}, nil
}

func currentShellHomeDirs() ([]string, error) {
	activeHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(activeHome) {
		return nil, fmt.Errorf("home directory must be absolute: %s", activeHome)
	}
	return []string{filepath.Clean(activeHome)}, nil
}

func validateWrapperDir(wrapperDir string, homeDirs []string) (string, error) {
	resolvedWrapper, err := resolveWrapperDir(wrapperDir)
	if err != nil {
		return "", err
	}
	withinHome, err := pathWithinAny(homeDirs, resolvedWrapper)
	if err != nil {
		return "", err
	}
	if !withinHome {
		if err := validateOwnedWrapperDir(resolvedWrapper); err != nil {
			return "", err
		}
	}
	return resolvedWrapper, nil
}

func resolveWrapperDir(wrapperDir string) (string, error) {
	if !filepath.IsAbs(wrapperDir) {
		return "", fmt.Errorf("wrapper directory must be absolute: %s", wrapperDir)
	}
	resolvedWrapper, err := resolvePath(wrapperDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve wrapper directory: %w", err)
	}
	if filepath.Dir(resolvedWrapper) == resolvedWrapper {
		return "", fmt.Errorf("wrapper directory cannot be a filesystem root")
	}
	return resolvedWrapper, nil
}

func pathWithinAny(parents []string, child string) (bool, error) {
	for _, parent := range parents {
		resolvedParent, err := resolvePath(parent)
		if err != nil {
			return false, fmt.Errorf("failed to resolve home directory: %w", err)
		}
		within, err := pathWithin(resolvedParent, child)
		if err != nil {
			return false, err
		}
		if within {
			return true, nil
		}
	}
	return false, nil
}

func validateOwnedWrapperDir(path string) error {
	info, err := safefs.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect wrapper directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("wrapper directory is not a directory: %s", path)
	}
	return validateWrapperDirOwner(path, info)
}

func validateWrapperDirOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to inspect wrapper directory owner: %s", path)
	}
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to find current user: %w", err)
	}
	ownerUID := strconv.FormatUint(uint64(stat.Uid), 10)
	if ownerUID != currentUser.Uid {
		return fmt.Errorf("wrapper directory is not owned by the current user: %s", path)
	}
	return nil
}

func pathWithin(parent, child string) (bool, error) {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false, fmt.Errorf("failed to compare paths: %w", err)
	}
	outside := relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
	return !outside, nil
}

func resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if os.IsNotExist(err) {
		parent, parentErr := resolvePath(filepath.Dir(absolute))
		if parentErr != nil {
			return "", parentErr
		}
		return filepath.Join(parent, filepath.Base(absolute)), nil
	}
	return resolved, err
}

func removeGeneratedWrappers(wrapperDir string) error {
	entries, err := os.ReadDir(wrapperDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read wrapper directory: %w", err)
	}
	var cleanupErr error
	for _, entry := range entries {
		cleanupErr = errors.Join(cleanupErr, removeGeneratedWrapper(wrapperDir, entry))
	}
	return errors.Join(cleanupErr, removeWrapperDelegates(wrapperDir))
}

func removeMissingToolWrappers(wrapperDir string) error {
	entries, err := os.ReadDir(wrapperDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := removeMissingToolWrapper(wrapperDir, entry); err != nil {
			return err
		}
	}
	return nil
}

func removeMissingToolWrapper(dir string, entry os.DirEntry) error {
	if !entry.Type().IsRegular() {
		return nil
	}
	path := filepath.Join(dir, entry.Name())
	data, err := safefs.ReadFile(path)
	if err != nil {
		return err
	}
	original := generatedWrapperOriginal(string(data))
	if original == "" {
		return nil
	}
	if _, err := os.Stat(original); os.IsNotExist(err) {
		return os.Remove(path)
	}
	return nil
}

func generatedWrapperOriginal(content string) string {
	if !isGeneratedWrapper(content) {
		return ""
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, `ORIGINAL="`) {
			return unquoteWrapperPath(strings.TrimPrefix(line, "ORIGINAL="))
		}
		if strings.HasPrefix(line, `ORIGINAL_BINARY="`) {
			return unquoteWrapperPath(strings.TrimPrefix(line, "ORIGINAL_BINARY="))
		}
	}
	return ""
}

func unquoteWrapperPath(value string) string {
	if !strings.HasSuffix(value, `"`) {
		return ""
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
	replacer := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, `$`, "\\`", "`")
	path := replacer.Replace(value)
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func removeGeneratedWrapper(wrapperDir string, entry os.DirEntry) error {
	if !entry.Type().IsRegular() {
		return nil
	}
	path := filepath.Join(wrapperDir, entry.Name())
	content, err := safefs.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read wrapper %s: %w", entry.Name(), err)
	}
	if !isGeneratedWrapper(string(content)) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove wrapper %s: %w", entry.Name(), err)
	}
	return nil
}

func isGeneratedWrapper(content string) bool {
	commonFields := []string{`DIU_BINARY="diu"`, "DIU_SOCKET=", "DIU_TOOL="}
	if !strings.HasPrefix(content, "#!/bin/bash\n") {
		return false
	}
	if !containsAll(content, commonFields) {
		return false
	}
	currentPrefix := "#!/bin/bash\n" + core.GeneratedWrapperMarker + "\n"
	if strings.HasPrefix(content, currentPrefix) {
		return hasWrapperOriginal(content)
	}
	return isLegacyWrapper(content)
}

func hasWrapperOriginal(content string) bool {
	return strings.Contains(content, "ORIGINAL=") || strings.Contains(content, "ORIGINAL_BINARY=")
}

func isLegacyWrapper(content string) bool {
	legacyFields := []string{
		"json_escape() {",
		`DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"`,
		`"$DIU_RECORD_BINARY" record`,
		"exit $EXIT_CODE",
	}
	return hasWrapperOriginal(content) && containsAll(content, legacyFields)
}

func containsAll(content string, fields []string) bool {
	for _, field := range fields {
		if !strings.Contains(content, field) {
			return false
		}
	}
	return true
}

func removeShellPathEntriesFromHomes(homeDirs []string, wrapperDir string) error {
	var cleanupErr error
	for _, homeDir := range homeDirs {
		cleanupErr = errors.Join(cleanupErr, removeShellPathEntries(homeDir, wrapperDir))
	}
	return cleanupErr
}

func removeShellPathEntries(homeDir, wrapperDir string) error {
	fishPath := filepath.Join(homeDir, ".config", "fish", "config.fish")
	fishLine := core.FishPathLine(wrapperDir)
	legacyFishLine := strings.ReplaceAll(fishLine, "`", "\\`")
	entries := []struct {
		path string
		line string
	}{
		{filepath.Join(homeDir, ".bashrc"), core.PosixPathLine(wrapperDir)},
		{filepath.Join(homeDir, ".zshrc"), core.PosixPathLine(wrapperDir)},
		{fishPath, fishLine},
		{fishPath, legacyFishLine},
	}
	var cleanupErr error
	for _, entry := range entries {
		cleanupErr = errors.Join(cleanupErr, removeShellPathEntry(entry.path, entry.line))
	}
	return cleanupErr
}

func removeShellPathEntry(path, line string) error {
	content, err := safefs.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read shell config %s: %w", path, err)
	}
	updated := removeShellPathBlock(string(content), line)
	if updated == string(content) {
		return nil
	}
	if err := writePrivateFile(path, []byte(updated)); err != nil {
		return fmt.Errorf("failed to update shell config %s: %w", path, err)
	}
	return nil
}

func removeShellPathBlock(content, line string) string {
	block := core.ShellPathMarker + "\n" + line + "\n"
	for {
		index := strings.Index(content, block)
		if index < 0 {
			return content
		}
		start := shellBlockStart(content, index)
		end := index + len(block)
		content = joinShellConfig(content[:start], content[end:])
	}
}

func shellBlockStart(content string, index int) int {
	if index <= 0 {
		return index
	}
	if content[index-1] == '\n' {
		return index - 1
	}
	return index
}

func joinShellConfig(prefix, suffix string) string {
	hasPrefix := prefix != ""
	hasSuffix := suffix != ""
	prefixEndsLine := strings.HasSuffix(prefix, "\n")
	suffixStartsLine := strings.HasPrefix(suffix, "\n")
	hasBothParts := hasPrefix && hasSuffix
	alreadySeparated := prefixEndsLine || suffixStartsLine
	needsNewline := hasBothParts && !alreadySeparated
	if needsNewline {
		joined := prefix + "\n" + suffix
		return joined
	}
	return prefix + suffix
}

func writePrivateFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "")
	}()
	_, err = file.Write(data)
	return err
}

func refreshCommandWrappers(config *core.Config, activity *dx.Activity) error {
	if !config.Monitoring.Process.ShouldAutoInstallWrappers {
		return nil
	}
	warn := func(message string) { activity.Notice(dx.Warning, message) }
	if err := configureCommandWrappers(config, warn); err != nil {
		return err
	}
	return removeMissingToolWrappers(config.Monitoring.Process.WrapperDir)
}

func configureCommandWrappers(config *core.Config, warn func(string)) error {
	if !config.Monitoring.Process.ShouldAutoInstallWrappers {
		return removeDisabledWrappers(config)
	}
	if err := installWrappers(config, warn); err != nil {
		return err
	}
	return installExecutableWrappers(config)
}

func removeDisabledWrappers(config *core.Config) error {
	homes, err := currentShellHomeDirs()
	if err != nil {
		return err
	}
	configured := config.Monitoring.Process.WrapperDir
	dir, err := validateWrapperDir(configured, homes)
	if err != nil {
		return err
	}
	wrapperErr := removeGeneratedWrappers(dir)
	shellErr := removeShellPathEntriesFromHomes(homes, configured)
	return errors.Join(wrapperErr, shellErr)
}

func installWrappers(config *core.Config, warn func(string)) error {
	if warn == nil {
		warn = func(message string) { cliOutput().Status(dx.Warning, message) }
	}
	for _, tool := range config.Monitoring.EnabledTools {
		monitor, err := newMonitor(core.NormalizeToolName(tool))
		if err != nil {
			continue
		}
		if err := monitor.Initialize(config); err != nil {
			executableUnavailable := errors.Is(err, exec.ErrNotFound)
			if executableUnavailable {
				continue
			}
			warn(fmt.Sprintf("failed to install %s wrapper: %v", tool, err))
		}
	}
	return nil
}

func installExecutableWrappers(config *core.Config) error {
	if !config.Monitoring.Process.ShouldAutoInstallWrappers {
		return nil
	}
	targets := discoverExecutableWrappers(config)
	for _, target := range targets {
		if err := writeExecutableWrapper(config, target); err != nil {
			return err
		}
	}
	return nil
}

func discoverExecutableWrappers(config *core.Config) []executableWrapper {
	targets := make(map[string]executableWrapper)
	watchPaths := config.Monitoring.Filesystem.WatchPaths
	addToolExecutableDirs(config, targets, core.ToolHomebrew, watchPaths[core.ToolHomebrew])
	addToolExecutableDirs(config, targets, core.ToolNPM, executableDirs(npmGlobalBinDir(), watchPaths[core.ToolNPM]))
	addToolExecutableDirs(config, targets, core.ToolPNPM, executableDirs(pnpmGlobalBinDir(), watchPaths[core.ToolPNPM]))
	addToolExecutableDirs(config, targets, core.ToolBun, executableDirs(bunGlobalBinDir(), watchPaths[core.ToolBun]))
	addGoExecutableDirs(config, targets)
	addToolExecutableDirs(config, targets, core.ToolPip, executableDirs(pythonUserBaseBinDir(), watchPaths[core.ToolPip]))
	addToolExecutableDirs(config, targets, core.ToolUV, executableDirs(uvToolBinDir(), watchPaths[core.ToolUV]))

	return slices.SortedFunc(maps.Values(targets), func(a, b executableWrapper) int {
		return strings.Compare(a.Name, b.Name)
	})
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

func addToolExecutableDirs(config *core.Config, targets map[string]executableWrapper, tool string, dirs []string) {
	if !monitoringToolEnabled(config, tool) {
		return
	}
	for _, dir := range dirs {
		addExecutableDir(targets, tool, dir)
	}
}

func addGoExecutableDirs(config *core.Config, targets map[string]executableWrapper) {
	if !monitoringToolEnabled(config, core.ToolGo) {
		return
	}
	addExecutableDir(targets, core.ToolGoBinary, goBinaryDir(config))
}

func addExecutableDir(targets map[string]executableWrapper, tool, dir string) {
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

func addExecutableEntry(targets map[string]executableWrapper, tool, dir, name string) {
	if shouldSkipExecutableWrapper(name) {
		return
	}
	path := filepath.Join(dir, name)
	if !usableExecutablePath(path) {
		return
	}
	pkg := packageNameForExecutable(tool, path, name)
	skipTarget := pkg == "" || preferExistingExecutable(targets[name], path)
	if skipTarget {
		return
	}
	targets[name] = executableWrapper{
		Name:         name,
		OriginalPath: path,
		Tool:         tool,
		Package:      pkg,
	}
}

func preferExistingExecutable(existing executableWrapper, path string) bool {
	if existing.OriginalPath == "" {
		return false
	}
	return executablePathPriority(existing.OriginalPath) <= executablePathPriority(path)
}

func executablePathPriority(path string) int {
	paths := filepath.SplitList(os.Getenv("PATH"))
	for index, dir := range paths {
		candidate := filepath.Join(dir, filepath.Base(path))
		if sameExecutable(candidate, path) {
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

func writeExecutableWrapper(config *core.Config, target executableWrapper) error {
	wrapperPath, err := executableWrapperPath(config.Monitoring.Process.WrapperDir, target.Name)
	if err != nil {
		return err
	}
	script := executableWrapperScript(config, target)
	return writeOwnerExecutableFile(wrapperPath, []byte(script))
}

func executableWrapperScript(config *core.Config, target executableWrapper) string {
	marker := core.GeneratedWrapperMarker
	socket := core.ShellEscapeString(config.Daemon.SocketPath)
	original := core.ShellEscapeString(target.OriginalPath)
	tool := core.ShellEscapeString(target.Tool)
	pkg := core.ShellEscapeString(target.Package)
	name := core.ShellEscapeString(target.Name)
	recording := core.WrapperRecordingScript(executableWrapperPayload)
	return fmt.Sprintf(executableWrapperScriptTemplate, marker, socket, "diu", original, tool, pkg, name, core.WrapperCommandGuard, recording)
}
