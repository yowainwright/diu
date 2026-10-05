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
	originalPath string
}

func NewProcessMonitor(name, binaryPath string) *ProcessMonitor {
	return &ProcessMonitor{
		BaseMonitor: NewBaseMonitor(name),
		binaryPath:  binaryPath,
	}
}

func (m *ProcessMonitor) Initialize(config *core.Config) error {
	if err := m.BaseMonitor.Initialize(config); err != nil {
		return err
	}

	originalPath, err := m.findOriginalBinary()
	if err == nil {
		m.originalPath = originalPath
		return nil
	}
	m.originalPath = m.binaryPath
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

func (m *ProcessMonitor) wrapperDir() string {
	monitoring := m.config.Monitoring
	process := monitoring.Process
	return process.WrapperDir
}

func (m *ProcessMonitor) cleanWrapperDir() string {
	wrapperDir := m.wrapperDir()
	return filepath.Clean(wrapperDir)
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
