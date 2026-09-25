package main

import (
	"fmt"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

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

func scanPackages(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Scanning installed packages")
	defer activity.Stop()
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if flagBool(cmd, "refresh-wrappers") {
		if err := refreshCommandWrappers(config, activity); err != nil {
			return err
		}
	}
	total, err := scanInventory(config, activity)
	if err != nil {
		return err
	}
	activity.Success(fmt.Sprintf("%d packages scanned", total))
	return nil
}

func scanInventory(config *core.Config, activity *dx.Activity) (int, error) {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return 0, fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)
	scanner, err := newPackageScanner(config, store, activity)
	if err != nil {
		return 0, err
	}
	err = scanner.run()
	return scanner.total, err
}

func newPackageScanner(config *core.Config, store storage.Storage, activity *dx.Activity) (*packageScanner, error) {
	existing, err := store.AllPackages()
	if err != nil {
		return nil, fmt.Errorf("failed to read package inventory: %w", err)
	}
	scanConfig := *config
	scanConfig.Monitoring.Process.ShouldAutoInstallWrappers = false
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
	isGoBinary := pkg.Tool == core.ToolGoBinary
	needsSignature := pkg.ModifiedAt == 0
	hasPath := pkg.Path != ""
	shouldPrepare := isGoBinary && needsSignature && hasPath
	if !shouldPrepare {
		return
	}
	err := populateGoBinarySignature(pkg)
	if err == nil {
		return
	}
	if s.activity != nil {
		s.activity.Notice(dx.Warning, err.Error())
	}
}

func (s *packageScanner) prepareGoFingerprint(pkg *core.PackageInfo) {
	isGoBinary := pkg.Tool == core.ToolGoBinary
	needsFingerprint := pkg.Fingerprint == ""
	hasPath := pkg.Path != ""
	shouldPrepare := isGoBinary && needsFingerprint && hasPath
	if !shouldPrepare {
		return
	}
	err := populateGoBinaryFingerprint(pkg)
	s.noticePackageScanError(err)
}

func (s *packageScanner) noticePackageScanError(err error) {
	if err == nil {
		return
	}
	if s.activity != nil {
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
	if err != nil {
		return fmt.Errorf("failed to inspect Go binary %s", pkg.Path)
	}
	if !info.Mode().IsRegular() {
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
	hasPackage := target.Package != ""
	trackExecutable := !alreadySeen && hasPackage
	return !trackExecutable
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
	if config.Tools.Homebrew.ShouldTrackCasks {
		return []string{core.ToolHomebrew, homebrewCaskTool}
	}
	return []string{core.ToolHomebrew}
}

func npmInventoryScopes(config *core.Config) []string {
	if !config.Tools.NPM.ShouldTrackGlobalOnly {
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
	if pkg.Fingerprint != "" {
		return
	}
	if preserveFingerprint {
		pkg.Fingerprint = existing.Fingerprint
	}
}

func mergePackageHistory(pkg, existing *core.PackageInfo) {
	if shouldPreserveInstallDate(pkg, existing) {
		pkg.InstallDate = existing.InstallDate
	}
	pkg.LastUsed = existing.LastUsed
	pkg.UsageCount = existing.UsageCount
}

func shouldPreserveInstallDate(pkg, existing *core.PackageInfo) bool {
	if pkg.InstallDate.IsZero() {
		return true
	}
	if existing.InstallDate.IsZero() {
		return false
	}
	return existing.InstallDate.Before(pkg.InstallDate)
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
	if pkg.Tool != core.ToolGoBinary {
		return false
	}
	if pkg.Path != existing.Path {
		return false
	}
	sameSize := pkg.SizeBytes == existing.SizeBytes
	if !sameSize {
		return false
	}
	if pkg.ModifiedAt == 0 {
		return false
	}
	return pkg.ModifiedAt == existing.ModifiedAt
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
