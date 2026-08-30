package monitors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

type ProcessMonitor struct {
	*BaseMonitor
	binaryPath   string
	wrapperPath  string
	originalPath string
	homeDir      string
}

type shellPathEntry struct {
	path string
	line string
}

const processWrapperScriptTemplate = `#!/bin/bash
%s
ORIGINAL="%s"
DIU_BINARY="%s"
DIU_SOCKET="%s"
DIU_TOOL="%s"
START_TIME=$(date +%%s)

"$ORIGINAL" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s)
DURATION=$(( (END_TIME - START_TIME) * 1000 ))

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%%s' "$value"
}

args_json="["
first=true
for arg in "$@"; do
    if [ "$first" = true ]; then
        first=false
    else
        args_json="$args_json,"
    fi
    args_json="$args_json\"$(json_escape "$arg")\""
done
args_json="$args_json]"

payload=$(cat <<EOF
{
    "tool": "$DIU_TOOL",
    "command": "$(json_escape "$DIU_TOOL $*")",
    "args": $args_json,
    "exit_code": $EXIT_CODE,
    "duration_ms": $DURATION,
    "timestamp": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
    "working_dir": "$(json_escape "$(pwd)")",
    "user": "$(json_escape "$(whoami)")",
    "metadata": {
        "original_path": "$(json_escape "$ORIGINAL")"
    }
}
EOF
)

record_fallback() {
    DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"
    if [ -n "$DIU_RECORD_BINARY" ] && [ -x "$DIU_RECORD_BINARY" ]; then
        printf '%%s\n' "$payload" | "$DIU_RECORD_BINARY" record >/dev/null 2>&1
    fi
}

if [ -S "$DIU_SOCKET" ] && command -v nc >/dev/null 2>&1; then
    {
        sent=false
        if printf '%%s\n' "$payload" | nc -w 1 -U "$DIU_SOCKET" 2>/dev/null; then
            sent=true
        fi

        if [ "$sent" != true ]; then
            record_fallback
        fi
    } &>/dev/null &
else
    record_fallback >/dev/null 2>&1
fi

exit $EXIT_CODE
`

func NewProcessMonitor(name, binaryPath string) *ProcessMonitor {
	homeDir := os.Getenv("HOME")
	if dir, err := os.UserHomeDir(); err == nil {
		homeDir = dir
	}
	return &ProcessMonitor{
		BaseMonitor: NewBaseMonitor(name),
		binaryPath:  binaryPath,
		homeDir:     homeDir,
	}
}

func (m *ProcessMonitor) Initialize(config *core.Config) error {
	if err := m.BaseMonitor.Initialize(config); err != nil {
		return err
	}

	m.wrapperPath = filepath.Join(config.Monitoring.Process.WrapperDir, filepath.Base(m.binaryPath))
	if err := m.setOriginalPath(config); err != nil {
		return err
	}
	if !config.Monitoring.Process.ShouldAutoInstallWrappers {
		return nil
	}
	return m.InstallWrapper()
}

func (m *ProcessMonitor) setOriginalPath(config *core.Config) error {
	originalPath, err := m.findOriginalBinary()
	if err != nil {
		if config.Monitoring.Process.ShouldAutoInstallWrappers {
			return err
		}
		m.originalPath = m.binaryPath
		return nil
	}
	m.originalPath = originalPath
	return nil
}

func (m *ProcessMonitor) findOriginalBinary() (string, error) {
	wrapperDir := m.cleanWrapperDir()
	if filepath.IsAbs(m.binaryPath) {
		return validateOriginalBinaryPath(m.binaryPath, wrapperDir)
	}

	paths := filepath.SplitList(os.Getenv("PATH"))
	for _, path := range paths {
		if skipSearchPath(path, wrapperDir) {
			continue
		}

		candidate := filepath.Join(path, filepath.Base(m.binaryPath))
		validatedPath, ok := executableCandidate(candidate, wrapperDir)
		if ok {
			return validatedPath, nil
		}
	}
	return "", fmt.Errorf("original binary %q not found in PATH outside wrapper directory %s: %w", filepath.Base(m.binaryPath), wrapperDir, exec.ErrNotFound)
}

func validateOriginalBinaryPath(binaryPath, wrapperDir string) (string, error) {
	validatedPath, err := validateExecutablePath(binaryPath)
	if err != nil {
		return "", err
	}
	if pathWithinDirectory(validatedPath, wrapperDir) {
		return "", fmt.Errorf("original binary %q resolves inside wrapper directory %s", validatedPath, wrapperDir)
	}
	return validatedPath, nil
}

func skipSearchPath(path, wrapperDir string) bool {
	isEmpty := path == ""
	isRelative := !filepath.IsAbs(path)
	isWrapperDir := filepath.Clean(path) == wrapperDir
	shouldSkip := isEmpty || isRelative || isWrapperDir
	return shouldSkip
}

func executableCandidate(candidate, wrapperDir string) (string, bool) {
	info, err := safefs.Stat(candidate)
	isDirectory := err == nil && info.IsDir()
	unusablePath := err != nil || isDirectory
	if unusablePath {
		return "", false
	}
	isExecutable := info.Mode()&core.ExecutableModeMask != 0
	if !isExecutable {
		return "", false
	}
	validatedPath, err := validateExecutablePath(candidate)
	if err != nil {
		return "", false
	}
	if pathWithinDirectory(validatedPath, wrapperDir) {
		return "", false
	}
	return validatedPath, true
}

func pathWithinDirectory(path, dir string) bool {
	cleanPath, cleanDir, ok := cleanDirectoryPaths(path, dir)
	if !ok {
		return false
	}
	relativePath, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	return relativePathInsideDirectory(relativePath)
}

func cleanDirectoryPaths(path, dir string) (string, string, bool) {
	pathEmpty := strings.TrimSpace(path) == ""
	dirEmpty := strings.TrimSpace(dir) == ""
	missingPath := pathEmpty || dirEmpty
	if missingPath {
		return "", "", false
	}

	cleanPath, ok := absoluteCleanPath(path)
	if !ok {
		return "", "", false
	}
	cleanDir, ok := absoluteCleanPath(dir)
	if !ok {
		return "", "", false
	}
	cleanPath = resolvedCleanPath(cleanPath)
	cleanDir = resolvedCleanPath(cleanDir)
	return cleanPath, cleanDir, true
}

func absoluteCleanPath(path string) (string, bool) {
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		return cleanPath, true
	}
	absPath, err := filepath.Abs(cleanPath)
	return absPath, err == nil
}

func resolvedCleanPath(path string) string {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolvedPath
}

func relativePathInsideDirectory(relativePath string) bool {
	if relativePath == "." {
		return true
	}
	if relativePath == ".." {
		return false
	}
	parentPrefix := ".." + string(filepath.Separator)
	insideSubtree := !strings.HasPrefix(relativePath, parentPrefix)
	return insideSubtree
}

func (m *ProcessMonitor) InstallWrapper() error {
	wrapperDir := m.wrapperDir()
	if err := os.MkdirAll(wrapperDir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create wrapper directory: %w", err)
	}

	wrapperContent := m.generateWrapperScript()
	if err := writeOwnerExecutableFile(m.wrapperPath, []byte(wrapperContent)); err != nil {
		return fmt.Errorf("failed to write wrapper script: %w", err)
	}

	return m.updateShellConfig()
}

func writeOwnerExecutableFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := file.Close()
		shouldReturnCloseErr := err == nil && closeErr != nil
		if shouldReturnCloseErr {
			err = closeErr
		}
	}()

	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Chmod(core.OwnerExecutableMode)
}

func (m *ProcessMonitor) generateWrapperScript() string {
	return generateProcessWrapperScript(m.originalPath, "diu", m.config.Daemon.SocketPath, m.name)
}

func generateProcessWrapperScript(originalPath, diuPath, socketPath, tool string) string {
	marker := core.GeneratedWrapperMarker
	original := core.ShellEscapeString(originalPath)
	diu := core.ShellEscapeString(diuPath)
	socket := core.ShellEscapeString(socketPath)
	escapedTool := core.ShellEscapeString(tool)
	return fmt.Sprintf(processWrapperScriptTemplate, marker, original, diu, socket, escapedTool)
}

func (m *ProcessMonitor) updateShellConfig() error {
	wrapperDir := m.wrapperDir()
	bashPath := filepath.Join(m.homeDir, ".bashrc")
	zshPath := filepath.Join(m.homeDir, ".zshrc")
	fishPath := filepath.Join(m.homeDir, ".config", "fish", "config.fish")
	posixLine := core.PosixPathLine(wrapperDir)
	fishLine := core.FishPathLine(wrapperDir)
	entries := []shellPathEntry{{bashPath, posixLine}, {zshPath, posixLine}, {fishPath, fishLine}}
	for _, entry := range entries {
		if err := appendPathConfigIfPresent(entry.path, entry.line); err != nil {
			return err
		}
	}
	return nil
}

func (m *ProcessMonitor) wrapperDir() string {
	monitoring := m.config.Monitoring
	process := monitoring.Process
	return process.WrapperDir
}

func (m *ProcessMonitor) cleanWrapperDir() string {
	wrapperDir := m.wrapperDir()
	return filepath.Clean(wrapperDir)
}

func appendPathConfigIfPresent(path, line string) error {
	content, err := readShellConfigIfPresent(path)
	if err != nil {
		return err
	}
	if content == nil {
		return nil
	}
	if strings.Contains(string(content), line) {
		return nil
	}
	lineWithNewline := line + "\n"
	if err := appendShellConfigLines(path, "\n"+core.ShellPathMarker+"\n", lineWithNewline); err != nil {
		return fmt.Errorf("failed to update shell config %s: %w", path, err)
	}
	return nil
}

func readShellConfigIfPresent(path string) ([]byte, error) {
	if _, err := safefs.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to inspect shell config %s: %w", path, err)
	}
	content, err := safefs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read shell config %s: %w", path, err)
	}
	return content, nil
}

func appendShellConfigLines(path string, lines ...string) (err error) {
	file, err := safefs.OpenFile(path, os.O_APPEND|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := file.Close()
		shouldReturnCloseErr := err == nil && closeErr != nil
		if shouldReturnCloseErr {
			err = closeErr
		}
	}()

	for _, line := range lines {
		if _, err := file.WriteString(line); err != nil {
			return err
		}
	}
	return nil
}

func (m *ProcessMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	return nil
}

func (m *ProcessMonitor) ExecuteAndTrack(cmd string, args []string) (*core.ExecutionRecord, error) {
	startTime := time.Now()
	command, err := m.trackedCommand(args)
	if err != nil {
		return nil, err
	}
	err = command.Run()
	exitCode := commandExitCode(err)
	duration := time.Since(startTime)
	record := m.newExecutionRecord(cmd, args, startTime, duration, exitCode)
	m.applyParsedCommand(record, cmd, args)
	return record, nil
}

func (m *ProcessMonitor) trackedCommand(args []string) (*exec.Cmd, error) {
	originalPath, err := validateExecutablePath(m.originalPath)
	if err != nil {
		return nil, err
	}
	command := exec.Command(originalPath, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	return command, nil
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 0
	}
	return exitErr.ExitCode()
}

func (m *ProcessMonitor) newExecutionRecord(cmd string, args []string, start time.Time, duration time.Duration, exitCode int) *core.ExecutionRecord {
	workingDir, _ := os.Getwd()
	return &core.ExecutionRecord{
		ID:         fmt.Sprintf("exec_%s_%d", time.Now().Format("20060102_150405"), time.Now().UnixNano()),
		Tool:       m.name,
		Command:    fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")),
		Args:       args,
		Timestamp:  start,
		Duration:   duration,
		ExitCode:   exitCode,
		WorkingDir: workingDir,
		User:       currentUsername(),
	}
}

func currentUsername() string {
	usr, err := user.Current()
	if err != nil {
		return ""
	}
	return usr.Username
}

func (m *ProcessMonitor) applyParsedCommand(record *core.ExecutionRecord, cmd string, args []string) {
	parsed, err := m.ParseCommand(cmd, args)
	if err != nil {
		return
	}
	record.PackagesAffected = parsed.PackagesAffected
	record.Metadata = parsed.Metadata
}

func validateExecutablePath(path string) (string, error) {
	cleanPath, err := cleanExecutablePath(path)
	if err != nil {
		return "", err
	}
	resolvedPath, err := resolveExecutablePath(cleanPath)
	if err != nil {
		return "", err
	}
	return inspectExecutablePath(resolvedPath)
}

func cleanExecutablePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
	}
	return cleanPath, nil
}

func resolveExecutablePath(cleanPath string) (string, error) {
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable %s: %w", cleanPath, err)
	}
	return resolvedPath, nil
}

func inspectExecutablePath(resolvedPath string) (string, error) {
	info, err := safefs.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", resolvedPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("executable path is a directory: %s", resolvedPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return "", fmt.Errorf("executable path is not executable: %s", resolvedPath)
	}

	return resolvedPath, nil
}

//nolint:legibility // Monitor interface requires this method name.
func (m *ProcessMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, fmt.Errorf("not implemented for base process monitor")
}

func (m *ProcessMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return &core.ExecutionRecord{
		Tool:    m.name,
		Command: cmd,
		Args:    args,
	}, nil
}
