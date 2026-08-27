package main

import (
	"encoding/json"
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
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

// executableWrapper represents an executable to be wrapped
type executableWrapper struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

type uninstallPaths struct {
	homeDirs        []string
	wrapperDir      string
	shellWrapperDir string
}

type inventoryScan struct {
	seen      map[string]map[string]struct{}
	startedAt time.Time
}

type packageScanner struct {
	config          *core.Config
	scanConfig      core.Config
	store           storage.Storage
	activity        *dx.Activity
	scan            *inventoryScan
	existing        map[string]map[string]*core.PackageInfo
	packages        []*core.PackageInfo
	scannedPackages map[string]*core.PackageInfo
	seenExecutables map[string]bool
	total           int
}

type packageReconciler interface {
	ReconcilePackages(map[string]map[string]struct{}, time.Time) error
}

type packageBatchUpdater interface {
	UpdatePackages([]*core.PackageInfo) error
}

type packageScanApplier interface {
	ApplyPackageScan([]*core.PackageInfo, map[string]map[string]struct{}, time.Time) error
}

const (
	fallbackRecordLockSuffix   = ".fallback.lock"
	fallbackRecordLockAttempts = 5
	fallbackRecordRetryDelay   = 10 * time.Millisecond
)

func newInventoryScan() *inventoryScan {
	return &inventoryScan{
		seen:      make(map[string]map[string]struct{}),
		startedAt: time.Now(),
	}
}

func (s *inventoryScan) complete(scopes []string) {
	for _, scope := range scopes {
		if s.seen[scope] == nil {
			s.seen[scope] = make(map[string]struct{})
		}
	}
}

func (s *inventoryScan) add(pkg *core.PackageInfo) {
	if s.seen[pkg.Tool] != nil {
		s.seen[pkg.Tool][pkg.Name] = struct{}{}
	}
}

// setupProject initializes DIU storage and wrappers
func setupProject(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Setting up DIU")
	defer activity.Stop()
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirectories(); err != nil {
		return err
	}
	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to initialize storage: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("failed to close storage: %w", err)
	}

	warn := func(message string) { activity.Notice(dx.Warning, message) }
	if err := installWrappers(config, warn); err != nil {
		return err
	}
	if err := installExecutableWrappers(config); err != nil {
		return err
	}

	activity.Success("DIU setup completed")
	return nil
}

func uninstallProject(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Removing DIU setup")
	defer activity.Stop()
	paths, err := loadUninstallPaths()
	if err != nil {
		return err
	}
	if err := removeGeneratedWrappers(paths.wrapperDir); err != nil {
		return err
	}
	if err := removeShellPathEntriesFromHomes(paths.homeDirs, paths.shellWrapperDir); err != nil {
		return err
	}
	activity.Success("DIU setup removed; configuration and usage data preserved")
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
	return uninstallPaths{homeDirs: homeDirs, wrapperDir: wrapperDir, shellWrapperDir: shellWrapperDir}, nil
}

func currentShellHomeDirs() ([]string, error) {
	activeHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	legacyHome := ""
	if currentUser, userErr := user.Current(); userErr == nil {
		legacyHome = currentUser.HomeDir
	}
	return shellHomeDirs(activeHome, legacyHome), nil
}

func shellHomeDirs(activeHome, legacyHome string) []string {
	activeHome = filepath.Clean(activeHome)
	homeDirs := []string{activeHome}
	if strings.TrimSpace(legacyHome) == "" {
		return homeDirs
	}
	legacyHome = filepath.Clean(legacyHome)
	if legacyHome != activeHome {
		homeDirs = append(homeDirs, legacyHome)
	}
	return homeDirs
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
	for _, entry := range entries {
		if err := removeGeneratedWrapper(wrapperDir, entry); err != nil {
			return err
		}
	}
	return nil
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
	if !strings.HasPrefix(content, "#!/bin/bash\n") || !containsAll(content, commonFields) {
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
	for _, homeDir := range homeDirs {
		if err := removeShellPathEntries(homeDir, wrapperDir); err != nil {
			return err
		}
	}
	return nil
}

func removeShellPathEntries(homeDir, wrapperDir string) error {
	entries := []struct {
		path string
		line string
	}{
		{filepath.Join(homeDir, ".bashrc"), core.PosixPathLine(wrapperDir)},
		{filepath.Join(homeDir, ".zshrc"), core.PosixPathLine(wrapperDir)},
		{filepath.Join(homeDir, ".config", "fish", "config.fish"), core.FishPathLine(wrapperDir)},
	}
	for _, entry := range entries {
		if err := removeShellPathEntry(entry.path, entry.line); err != nil {
			return err
		}
	}
	return nil
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
	if index > 0 && content[index-1] == '\n' {
		return index - 1
	}
	return index
}

func joinShellConfig(prefix, suffix string) string {
	needsNewline := prefix != "" && suffix != "" && !strings.HasSuffix(prefix, "\n") && !strings.HasPrefix(suffix, "\n")
	if needsNewline {
		return prefix + "\n" + suffix
	}
	return prefix + suffix
}

func writePrivateFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
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
	_, err = file.Write(data)
	return err
}

// scanPackages scans for installed packages
func scanPackages(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Scanning installed packages")
	defer activity.Stop()
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)
	scanner, err := newPackageScanner(config, store, activity)
	if err != nil {
		return err
	}
	return scanner.run()
}

func newPackageScanner(config *core.Config, store storage.Storage, activity *dx.Activity) (*packageScanner, error) {
	existing, err := store.GetAllPackages()
	if err != nil {
		return nil, fmt.Errorf("failed to read package inventory: %w", err)
	}
	scanConfig := *config
	scanConfig.Monitoring.Process.AutoInstallWrappers = false
	return &packageScanner{
		config:          config,
		scanConfig:      scanConfig,
		store:           store,
		activity:        activity,
		scan:            newInventoryScan(),
		existing:        existing,
		scannedPackages: make(map[string]*core.PackageInfo),
		seenExecutables: make(map[string]bool),
	}, nil
}

func (s *packageScanner) run() error {
	s.scanManagers()
	s.scanExecutables()
	if err := commitPackageScan(s.store, s.packages, s.scan); err != nil {
		return err
	}
	s.activity.Success(fmt.Sprintf("%d packages scanned", s.total))
	return nil
}

func (s *packageScanner) scanManagers() {
	for _, tool := range s.scanConfig.Monitoring.EnabledTools {
		s.scanManager(tool)
	}
}

func (s *packageScanner) scanManager(tool string) {
	s.activity.Update("Scanning " + tool + " packages")
	normalizedTool := core.NormalizeToolName(tool)
	monitor, err := newMonitor(normalizedTool)
	if err != nil {
		return
	}
	if err := monitor.Initialize(&s.scanConfig); err != nil {
		s.noticeScanFailure("initialize", tool, err)
		return
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		s.noticeScanFailure("scan", tool, err)
		return
	}
	s.scan.complete(inventoryScopes(normalizedTool, &s.scanConfig))
	s.addPackages(packages)
}

func (s *packageScanner) noticeScanFailure(action, tool string, err error) {
	message := fmt.Sprintf("failed to %s %s packages: %v", action, tool, err)
	s.activity.Notice(dx.Warning, message)
}

func (s *packageScanner) addPackages(packages []*core.PackageInfo) {
	for _, pkg := range packages {
		s.addPackage(pkg)
	}
}

func (s *packageScanner) addPackage(pkg *core.PackageInfo) {
	key := pkg.Tool + "/" + pkg.Name
	if scanned := s.scannedPackages[key]; scanned != nil {
		mergePackageDetails(scanned, pkg)
		return
	}
	s.prepareGoSignature(pkg)
	mergeExistingPackage(s.existing, pkg)
	s.prepareGoFingerprint(pkg)
	addPackageToInventory(s.existing, pkg)
	s.scannedPackages[key] = pkg
	s.packages = append(s.packages, pkg)
	s.scan.add(pkg)
	s.total++
}

func (s *packageScanner) prepareGoSignature(pkg *core.PackageInfo) {
	if pkg.Tool != core.ToolGoBinary || pkg.ModifiedAt != 0 || pkg.Path == "" {
		return
	}
	err := populateGoBinarySignature(pkg)
	if err != nil && s.activity != nil {
		s.activity.Notice(dx.Warning, err.Error())
	}
}

func (s *packageScanner) prepareGoFingerprint(pkg *core.PackageInfo) {
	if pkg.Tool != core.ToolGoBinary || pkg.Fingerprint != "" || pkg.Path == "" {
		return
	}
	err := populateGoBinaryFingerprint(pkg)
	if err != nil && s.activity != nil {
		s.activity.Notice(dx.Warning, err.Error())
	}
}

func populateGoBinaryFingerprint(pkg *core.PackageInfo) error {
	if err := populateGoBinarySignature(pkg); err != nil {
		return err
	}
	fingerprint, err := safefs.SHA256(pkg.Path)
	if err != nil {
		return fmt.Errorf("failed to fingerprint Go binary %s: %w", pkg.Path, err)
	}
	pkg.Fingerprint = fingerprint
	return nil
}

func populateGoBinarySignature(pkg *core.PackageInfo) error {
	if pkg.ModifiedAt != 0 {
		return nil
	}
	info, err := safefs.Lstat(pkg.Path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("failed to inspect Go binary %s", pkg.Path)
	}
	pkg.SizeBytes = info.Size()
	pkg.ModifiedAt = info.ModTime().UnixNano()
	return nil
}

func (s *packageScanner) scanExecutables() {
	for _, target := range discoverExecutableWrappers(s.config) {
		if s.executableAlreadySeen(target) {
			continue
		}
		s.addPackage(packageFromExecutable(target))
	}
}

func (s *packageScanner) executableAlreadySeen(target executableWrapper) bool {
	key := target.Tool + "/" + target.Package
	alreadySeen := s.seenExecutables[key]
	s.seenExecutables[key] = true
	return alreadySeen || target.Package == ""
}

func packageFromExecutable(target executableWrapper) *core.PackageInfo {
	return &core.PackageInfo{
		Name:        target.Package,
		Tool:        target.Tool,
		InstallDate: time.Now(),
		Path:        target.OriginalPath,
	}
}

func inventoryScopes(tool string, config *core.Config) []string {
	switch tool {
	case core.ToolHomebrew:
		return homebrewInventoryScopes(config)
	case core.ToolGo:
		return []string{core.ToolGoBinary, core.ToolGo}
	case core.ToolNPM:
		return npmInventoryScopes(config)
	case core.ToolPoetry:
		return nil
	default:
		return []string{tool}
	}
}

func homebrewInventoryScopes(config *core.Config) []string {
	if config.Tools.Homebrew.TrackCasks {
		return []string{core.ToolHomebrew, homebrewCaskTool}
	}
	return []string{core.ToolHomebrew}
}

func npmInventoryScopes(config *core.Config) []string {
	if !config.Tools.NPM.TrackGlobalOnly {
		return nil
	}
	return []string{core.ToolNPM}
}

func mergeExistingPackage(inventory map[string]map[string]*core.PackageInfo, pkg *core.PackageInfo) {
	existing := existingPackageFor(inventory, pkg)
	if existing == nil {
		return
	}
	preserveGoFingerprint := unchangedGoBinary(pkg, existing)
	mergePackageHistory(pkg, existing)
	mergePackageDetails(pkg, existing)
	preserveFingerprint := pkg.Tool != core.ToolGoBinary || preserveGoFingerprint
	if pkg.Fingerprint == "" && preserveFingerprint {
		pkg.Fingerprint = existing.Fingerprint
	}
}

func mergePackageHistory(pkg, existing *core.PackageInfo) {
	if pkg.InstallDate.IsZero() || (!existing.InstallDate.IsZero() && existing.InstallDate.Before(pkg.InstallDate)) {
		pkg.InstallDate = existing.InstallDate
	}
	pkg.LastUsed = existing.LastUsed
	pkg.UsageCount = existing.UsageCount
}

func mergePackageDetails(pkg, existing *core.PackageInfo) {
	if pkg.Version == "" {
		pkg.Version = existing.Version
	}
	if pkg.Path == "" {
		pkg.Path = existing.Path
	}
}

func unchangedGoBinary(pkg, existing *core.PackageInfo) bool {
	if pkg.Tool != core.ToolGoBinary || pkg.Path != existing.Path {
		return false
	}
	sameSize := pkg.SizeBytes == existing.SizeBytes
	sameModification := pkg.ModifiedAt != 0 && pkg.ModifiedAt == existing.ModifiedAt
	return sameSize && sameModification
}

func existingPackageFor(inventory map[string]map[string]*core.PackageInfo, pkg *core.PackageInfo) *core.PackageInfo {
	current := inventory[pkg.Tool][pkg.Name]
	if pkg.Tool != core.ToolGoBinary {
		return current
	}
	legacy := inventory[core.ToolGo][pkg.Name]
	return combinePackageHistory(current, legacy)
}

func combinePackageHistory(current, legacy *core.PackageInfo) *core.PackageInfo {
	if current == nil {
		return legacy
	}
	if legacy == nil {
		return current
	}
	combined := copyPackageInfo(current)
	combined.UsageCount += legacy.UsageCount
	if legacy.LastUsed.After(combined.LastUsed) {
		combined.LastUsed = legacy.LastUsed
	}
	mergeLegacyInstallDate(combined, legacy)
	return combined
}

func mergeLegacyInstallDate(combined, legacy *core.PackageInfo) {
	legacyInstallKnown := !legacy.InstallDate.IsZero()
	missingInstallDate := combined.InstallDate.IsZero()
	legacyInstallIsOlder := legacy.InstallDate.Before(combined.InstallDate)
	useLegacyInstallDate := legacyInstallKnown && (missingInstallDate || legacyInstallIsOlder)
	if useLegacyInstallDate {
		combined.InstallDate = legacy.InstallDate
	}
}

func copyPackageInfo(pkg *core.PackageInfo) *core.PackageInfo {
	copy := *pkg
	copy.Dependencies = append([]string(nil), pkg.Dependencies...)
	return &copy
}

func addPackageToInventory(inventory map[string]map[string]*core.PackageInfo, pkg *core.PackageInfo) {
	if inventory[pkg.Tool] == nil {
		inventory[pkg.Tool] = make(map[string]*core.PackageInfo)
	}
	inventory[pkg.Tool][pkg.Name] = pkg
}

func updateScannedPackages(store storage.Storage, packages []*core.PackageInfo) error {
	if len(packages) == 0 {
		return nil
	}
	if batchStore, ok := store.(packageBatchUpdater); ok {
		if err := batchStore.UpdatePackages(packages); err != nil {
			return fmt.Errorf("failed to update package inventory: %w", err)
		}
		return nil
	}
	for _, pkg := range packages {
		if err := store.UpdatePackage(pkg); err != nil {
			return fmt.Errorf("failed to update package %s/%s: %w", pkg.Tool, pkg.Name, err)
		}
	}
	return nil
}

func commitPackageScan(store storage.Storage, packages []*core.PackageInfo, scan *inventoryScan) error {
	if applier, ok := store.(packageScanApplier); ok {
		return applyPackageScan(applier, packages, scan)
	}
	if err := updateScannedPackages(store, packages); err != nil {
		return err
	}
	if reconciler, ok := store.(packageReconciler); ok {
		return reconcilePackageScan(reconciler, scan)
	}
	return nil
}

func applyPackageScan(applier packageScanApplier, packages []*core.PackageInfo, scan *inventoryScan) error {
	if err := applier.ApplyPackageScan(packages, scan.seen, scan.startedAt); err != nil {
		return fmt.Errorf("failed to apply package scan: %w", err)
	}
	return nil
}

func reconcilePackageScan(reconciler packageReconciler, scan *inventoryScan) error {
	if err := reconciler.ReconcilePackages(scan.seen, scan.startedAt); err != nil {
		return fmt.Errorf("failed to reconcile package inventory: %w", err)
	}
	return nil
}

// cleanup cleans up old execution records
func cleanup(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Cleaning execution history")
	defer activity.Stop()
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)

	if err := store.Cleanup(time.Time{}); err != nil {
		return fmt.Errorf("cleanup failed: %w", err)
	}

	activity.Success("Cleanup completed")
	return nil
}

// backup creates a manual backup
func backup(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Creating backup")
	defer activity.Stop()
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)

	if err := store.Backup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	activity.Success("Backup created")
	return nil
}

// recordExecution records an execution event from stdin
func recordExecution(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return withFallbackRecordLock(config, func() error {
		return storeFallbackExecution(config)
	})
}

func storeFallbackExecution(config *core.Config) error {
	var record core.ExecutionRecord
	if err := json.NewDecoder(cliOutput().Stdin()).Decode(&record); err != nil {
		return fmt.Errorf("failed to decode execution record: %w", err)
	}

	enrichExecutionRecord(config, &record)

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.AddExecution(&record); err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

func withFallbackRecordLock(config *core.Config, record func() error) (err error) {
	lock, acquired, err := acquireFallbackRecordLock(config.Storage.JSONFile)
	if err != nil {
		return err
	}
	if !acquired {
		_ = observability.MarkFallbackContention(config.Daemon.DataDir)
		return fmt.Errorf("fallback recorder remained busy after %d attempts", fallbackRecordLockAttempts)
	}
	defer func() {
		err = errors.Join(err, releaseFallbackRecordLock(lock))
	}()
	return record()
}

func acquireFallbackRecordLock(storagePath string) (*os.File, bool, error) {
	for attempt := 0; attempt < fallbackRecordLockAttempts; attempt++ {
		lock, acquired, err := tryAcquireFallbackRecordLock(storagePath)
		if err != nil || acquired {
			return lock, acquired, err
		}
		if attempt+1 < fallbackRecordLockAttempts {
			time.Sleep(fallbackRecordRetryDelay)
		}
	}
	return nil, false, nil
}

func tryAcquireFallbackRecordLock(storagePath string) (*os.File, bool, error) {
	lock, err := openFallbackRecordLock(storagePath)
	if err != nil {
		return nil, false, err
	}
	if err := lockFallbackRecorder(lock); err != nil {
		_ = lock.Close()
		if isFallbackLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to lock fallback recorder: %w", err)
	}
	return lock, true, nil
}

func openFallbackRecordLock(storagePath string) (*os.File, error) {
	lockPath := storagePath + fallbackRecordLockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), core.OwnerDirectoryMode); err != nil {
		return nil, fmt.Errorf("failed to create fallback lock directory: %w", err)
	}
	lock, err := safefs.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open fallback lock: %w", err)
	}
	if err := lock.Chmod(core.PrivateFileMode); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("failed to secure fallback lock: %w", err)
	}
	return lock, nil
}

func lockFallbackRecorder(lock *os.File) error {
	return syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func isFallbackLockContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func releaseFallbackRecordLock(lock *os.File) error {
	unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	closeErr := lock.Close()
	return errors.Join(unlockErr, closeErr)
}

// installWrappers installs monitors for enabled tools
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

// installExecutableWrappers installs wrappers for discovered executables
func installExecutableWrappers(config *core.Config) error {
	targets := discoverExecutableWrappers(config)
	for _, target := range targets {
		if err := writeExecutableWrapper(config, target); err != nil {
			return err
		}
	}
	return nil
}

// discoverExecutableWrappers discovers executables to wrap
func discoverExecutableWrappers(config *core.Config) []executableWrapper {
	targets := make(map[string]executableWrapper)
	toolEnabled := func(tool string) bool {
		for _, enabled := range config.Monitoring.EnabledTools {
			if core.NormalizeToolName(enabled) == tool {
				return true
			}
		}
		return false
	}
	addExecutableDir := func(tool, dir string) {
		if dir == "" {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if shouldSkipExecutableWrapper(name) {
				continue
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || info.Mode()&core.ExecutableModeMask == 0 {
				continue
			}
			if _, exists := targets[name]; exists {
				continue
			}
			targets[name] = executableWrapper{
				Name:         name,
				OriginalPath: path,
				Tool:         tool,
				Package:      packageNameForExecutable(tool, path, name),
			}
		}
	}

	if toolEnabled(core.ToolHomebrew) {
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolHomebrew] {
			addExecutableDir(core.ToolHomebrew, dir)
		}
	}
	if toolEnabled(core.ToolNPM) {
		if npmBin := npmGlobalBinDir(); npmBin != "" {
			addExecutableDir(core.ToolNPM, npmBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolNPM] {
			addExecutableDir(core.ToolNPM, dir)
		}
	}
	if toolEnabled(core.ToolPNPM) {
		if pnpmBin := pnpmGlobalBinDir(); pnpmBin != "" {
			addExecutableDir(core.ToolPNPM, pnpmBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolPNPM] {
			addExecutableDir(core.ToolPNPM, dir)
		}
	}
	if toolEnabled(core.ToolBun) {
		if bunBin := bunGlobalBinDir(); bunBin != "" {
			addExecutableDir(core.ToolBun, bunBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolBun] {
			addExecutableDir(core.ToolBun, dir)
		}
	}
	if toolEnabled(core.ToolGo) {
		if goBin := goBinaryDir(config); goBin != "" {
			addExecutableDir(core.ToolGoBinary, goBin)
		}
	}
	if toolEnabled(core.ToolPip) {
		if pythonBin := pythonUserBaseBinDir(); pythonBin != "" {
			addExecutableDir(core.ToolPip, pythonBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolPip] {
			addExecutableDir(core.ToolPip, dir)
		}
	}
	if toolEnabled(core.ToolUV) {
		if uvBin := uvToolBinDir(); uvBin != "" {
			addExecutableDir(core.ToolUV, uvBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolUV] {
			addExecutableDir(core.ToolUV, dir)
		}
	}

	return slices.SortedFunc(maps.Values(targets), func(a, b executableWrapper) int {
		return strings.Compare(a.Name, b.Name)
	})
}

// writeExecutableWrapper writes a wrapper script for an executable
func writeExecutableWrapper(config *core.Config, target executableWrapper) error {
	wrapperPath, err := executableWrapperPath(config.Monitoring.Process.WrapperDir, target.Name)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`#!/bin/bash
%s
DIU_SOCKET="%s"
DIU_BINARY="%s"
ORIGINAL_BINARY="%s"
DIU_TOOL="%s"
DIU_PACKAGE="%s"
DIU_EXECUTABLE="%s"
START_TIME=$(date +%%s)

"$ORIGINAL_BINARY" "$@"
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
        "command": "$(json_escape "$DIU_EXECUTABLE $*")",
        "args": $args_json,
        "exit_code": $EXIT_CODE,
        "duration_ms": $DURATION,
        "timestamp": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
        "working_dir": "$(json_escape "$(pwd)")",
        "user": "$(json_escape "$(whoami)")",
        "packages_affected": ["$(json_escape "$DIU_PACKAGE")"],
        "metadata": {
            "executable": "$(json_escape "$DIU_EXECUTABLE")",
            "original_path": "$(json_escape "$ORIGINAL_BINARY")"
        }
}
EOF
)

{
    sent=false
    if [ -S "$DIU_SOCKET" ] && command -v nc >/dev/null 2>&1; then
        if printf '%%s\n' "$payload" | nc -w 1 -U "$DIU_SOCKET" 2>/dev/null; then
            sent=true
        fi
    fi

    if [ "$sent" != true ]; then
        DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"
        if [ -n "$DIU_RECORD_BINARY" ] && [ -x "$DIU_RECORD_BINARY" ]; then
            printf '%%s\n' "$payload" | "$DIU_RECORD_BINARY" record >/dev/null 2>&1
        fi
    fi
} &>/dev/null &

exit $EXIT_CODE
`, core.GeneratedWrapperMarker, core.ShellEscapeString(config.Daemon.SocketPath), "diu", core.ShellEscapeString(target.OriginalPath), core.ShellEscapeString(target.Tool), core.ShellEscapeString(target.Package), core.ShellEscapeString(target.Name))

	return writeOwnerExecutableFile(wrapperPath, []byte(script))
}
