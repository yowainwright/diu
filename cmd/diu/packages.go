package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
)

// packageListOptions holds options for package listing
type packageListOptions struct {
	Tool   string
	Search string
	Unused string
	Limit  int
	Format string
}

// listPackages lists all tracked packages
func listPackages(cmd *command, _ []string) error {
	packages, err := loadTrackedPackages(cmd)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		printNoTrackedPackages()
		return nil
	}
	packages, done, err := filterTrackedUnused(cmd, packages)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	printTrackedPackages(packages)
	return nil
}

func loadTrackedPackages(cmd *command) ([]*core.PackageInfo, error) {
	store, err := openPackageStore()
	if err != nil {
		return nil, err
	}
	defer closeStore(store)

	tool, _ := cmd.Flags().GetString("tool")
	packages, err := packageListForTool(store, tool)
	if err != nil {
		return nil, err
	}
	sortPackages(packages)
	return packages, nil
}

func openPackageStore() (storage.Storage, error) {
	config, err := core.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage: %w", err)
	}
	return store, nil
}

func packageListForTool(store storage.Storage, tool string) ([]*core.PackageInfo, error) {
	tool = core.NormalizeToolName(tool)
	packages, err := store.GetPackages(tool)
	if err != nil {
		return nil, fmt.Errorf("failed to get packages: %w", err)
	}
	return packages, nil
}

func printNoTrackedPackages() {
	out := cliOutput()
	out.Println(out.DataStyle(dx.Info, "No packages tracked"))
}

func filterTrackedUnused(cmd *command, packages []*core.PackageInfo) ([]*core.PackageInfo, bool, error) {
	unusedStr, _ := cmd.Flags().GetString("unused")
	if unusedStr == "" {
		return packages, false, nil
	}
	duration, err := parseDuration(unusedStr)
	if err != nil {
		return nil, false, fmt.Errorf("invalid duration: %w", err)
	}

	cutoff := time.Now().Add(-duration)
	filtered := trackedPackagesUnusedBefore(packages, cutoff)
	if len(filtered) == 0 {
		printNoUnusedPackages()
		return filtered, true, nil
	}
	return filtered, false, nil
}

func trackedPackagesUnusedBefore(packages []*core.PackageInfo, cutoff time.Time) []*core.PackageInfo {
	var filtered []*core.PackageInfo
	for _, pkg := range packages {
		if pkg.LastUsed.Before(cutoff) {
			filtered = append(filtered, pkg)
		}
	}
	return filtered
}

func printNoUnusedPackages() {
	out := cliOutput()
	out.Println(out.DataStyle(dx.Success, "No unused packages found"))
}

func printTrackedPackages(packages []*core.PackageInfo) {
	out := cliOutput()
	out.Println(out.DataStyle(dx.Accent, "Tracked Packages"))
	out.Println()
	currentTool := ""
	for _, pkg := range packages {
		currentTool = printTrackedPackageGroup(out, currentTool, pkg)
		printTrackedPackage(out, pkg)
	}
}

func printTrackedPackageGroup(out *dx.Out, currentTool string, pkg *core.PackageInfo) string {
	if pkg.Tool == currentTool {
		return currentTool
	}
	out.Println()
	out.Println(out.DataStyle(dx.Accent, pkg.Tool))
	return pkg.Tool
}

func printTrackedPackage(out *dx.Out, pkg *core.PackageInfo) {
	out.Printf("  %s", pkg.Name)
	if pkg.Version != "" {
		out.Printf(" (%s)", pkg.Version)
	}
	lastUsed := "never"
	if !pkg.LastUsed.IsZero() {
		lastUsed = pkg.LastUsed.Format("2006-01-02")
	}
	out.Printf(" - used %d times, last: %s\n", pkg.UsageCount, lastUsed)
}

// checkPackages checks installed package usage
func checkPackages(cmd *command, args []string) error {
	opts := checkPackageOptions(cmd, args)
	if shouldUseInteractive(cmd, args) {
		allowUninstall := false
		return runPackageBrowser(allowUninstall)
	}

	packages, err := loadFilteredPackages(opts)
	if err != nil {
		return err
	}
	return printPackageList(packages, opts.Format)
}

func checkPackageOptions(cmd *command, args []string) packageListOptions {
	opts := packageListOptions{
		Tool:   flagString(cmd, "tool"),
		Search: flagString(cmd, "search"),
		Unused: flagString(cmd, "unused"),
		Limit:  flagInt(cmd, "limit"),
		Format: flagString(cmd, "format"),
	}
	if opts.Search == "" {
		if len(args) > 0 {
			opts.Search = strings.Join(args, " ")
		}
	}
	return opts
}

// managePackages searches and uninstalls installed packages
func managePackages(cmd *command, args []string) error {
	opts := managePackageOptions(cmd, args)
	if opts.uninstallName != "" {
		return uninstallByName(opts.uninstallName, opts.tool, opts.assumeYes, opts.dryRun)
	}

	if shouldUseInteractive(cmd, args) {
		allowUninstall := true
		return runPackageBrowser(allowUninstall)
	}
	return printManagedPackageList(opts)
}

func printManagedPackageList(opts managePackageOpts) error {
	packages, err := loadFilteredPackages(packageListOptions{
		Tool:   opts.tool,
		Search: opts.search,
		Limit:  defaultListLimit,
		Format: formatTable,
	})
	if err != nil {
		return err
	}
	return printPackageList(packages, formatTable)
}

type managePackageOpts struct {
	tool          string
	search        string
	uninstallName string
	assumeYes     bool
	dryRun        bool
}

func managePackageOptions(cmd *command, args []string) managePackageOpts {
	opts := managePackageOpts{
		tool:          flagString(cmd, "tool"),
		search:        flagString(cmd, "search"),
		uninstallName: flagString(cmd, "uninstall"),
		assumeYes:     flagBool(cmd, "yes"),
		dryRun:        flagBool(cmd, "dry-run"),
	}
	applyManageArgs(&opts, args)
	return opts
}

func applyManageArgs(opts *managePackageOpts, args []string) {
	if len(args) == 0 {
		return
	}
	if opts.search == "" {
		if opts.uninstallName == "" {
			opts.search = strings.Join(args, " ")
		}
	}
	if opts.uninstallName != "" {
		return
	}
	if opts.assumeYes {
		opts.uninstallName = strings.Join(args, " ")
	}
}

// shouldUseInteractive returns true if the command should use interactive mode
func shouldUseInteractive(cmd *command, args []string) bool {
	if len(args) > 0 {
		return false
	}
	if !isTerminal() {
		return false
	}
	used := false
	cmd.Flags().Visit(func(flag *flag) {
		used = true
	})
	return !used
}

// loadFilteredPackages loads packages from storage with filtering
func loadFilteredPackages(opts packageListOptions) ([]*core.PackageInfo, error) {
	store, err := openPackageStore()
	if err != nil {
		return nil, err
	}
	defer closeStore(store)

	packages, err := packageListForTool(store, opts.Tool)
	if err != nil {
		return nil, err
	}
	filtered, err := filterPackages(packages, opts)
	if err != nil {
		return nil, err
	}
	sortPackages(filtered)
	return applyPackageLimit(filtered, opts.Limit), nil
}

func applyPackageLimit(packages []*core.PackageInfo, limit int) []*core.PackageInfo {
	if limit <= 0 {
		return packages
	}
	if len(packages) <= limit {
		return packages
	}
	return packages[:limit]
}

// filterPackages filters packages by search and unused criteria
func filterPackages(packages []*core.PackageInfo, opts packageListOptions) ([]*core.PackageInfo, error) {
	cutoff, err := unusedPackageCutoff(opts.Unused)
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(opts.Search))
	var filtered []*core.PackageInfo
	for _, pkg := range packages {
		if packageExcludedBySearch(pkg, search) {
			continue
		}
		if packageUsedAfterCutoff(pkg, cutoff) {
			continue
		}
		filtered = append(filtered, pkg)
	}
	return filtered, nil
}

func unusedPackageCutoff(unused string) (time.Time, error) {
	if unused == "" {
		return time.Time{}, nil
	}
	duration, err := parseDuration(unused)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid unused duration: %w", err)
	}
	return time.Now().Add(-duration), nil
}

func packageExcludedBySearch(pkg *core.PackageInfo, search string) bool {
	if search == "" {
		return false
	}
	return !packageMatchesSearch(pkg, search)
}

func packageUsedAfterCutoff(pkg *core.PackageInfo, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return false
	}
	return !packageUnusedSince(pkg, cutoff)
}

// printPackageList prints a list of packages in the specified format
func printPackageList(packages []*core.PackageInfo, format string) error {
	out := cliOutput()
	switch format {
	case formatJSON:
		return printPackageJSON(out, packages)
	case formatCSV:
		return printPackageCSV(out, packages)
	default:
		return printPackageTable(out, packages)
	}
}

func printPackageJSON(out *dx.Out, packages []*core.PackageInfo) error {
	enc := json.NewEncoder(out.Stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(packages)
}

func printPackageCSV(out *dx.Out, packages []*core.PackageInfo) error {
	writer := csv.NewWriter(out.Stdout())
	if err := writer.Write([]string{"tool", "name", "version", "usage_count", "last_used", "path"}); err != nil {
		return err
	}
	for _, pkg := range packages {
		if err := writer.Write(packageCSVRow(pkg)); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func packageCSVRow(pkg *core.PackageInfo) []string {
	return []string{
		pkg.Tool,
		pkg.Name,
		pkg.Version,
		strconv.Itoa(pkg.UsageCount),
		formatLastUsed(pkg.LastUsed),
		pkg.Path,
	}
}

func printPackageTable(out *dx.Out, packages []*core.PackageInfo) error {
	if len(packages) == 0 {
		out.Println(out.DataStyle(dx.Info, "No packages found"))
		return nil
	}
	printPackageRows(packages, 0)
	return nil
}

// runPackageBrowser runs the interactive package browser
func runPackageBrowser(allowUninstall bool) error {
	packages, err := loadFilteredPackages(packageListOptions{})
	if err != nil {
		return err
	}
	out := cliOutput()
	reader := bufio.NewReader(out.Stdin())
	prompt := dx.NewPrompter(reader, out.Stderr())
	browser := packageBrowser{
		packages:       packages,
		reader:         reader,
		prompt:         prompt,
		allowUninstall: allowUninstall,
	}
	return browser.run()
}

type packageBrowser struct {
	packages       []*core.PackageInfo
	reader         *bufio.Reader
	prompt         *dx.Prompter
	search         string
	offset         int
	allowUninstall bool
}

func (b *packageBrowser) run() error {
	for {
		done, err := b.runStep()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (b *packageBrowser) runStep() (bool, error) {
	filtered, err := b.filteredPackages()
	if err != nil {
		return false, err
	}
	b.resetOffset(filtered)
	printBrowserScreen(filtered, b.offset, b.search, b.allowUninstall)
	input, err := b.prompt.Input("Action")
	if err != nil {
		return false, err
	}
	return b.handleInput(input, filtered)
}

func (b *packageBrowser) filteredPackages() ([]*core.PackageInfo, error) {
	filtered, err := filterPackages(b.packages, packageListOptions{Search: b.search})
	if err != nil {
		return nil, err
	}
	sortPackages(filtered)
	return filtered, nil
}

func (b *packageBrowser) resetOffset(filtered []*core.PackageInfo) {
	if b.offset >= len(filtered) {
		b.offset = 0
	}
}

func (b *packageBrowser) handleInput(input string, filtered []*core.PackageInfo) (bool, error) {
	switch input {
	case actionQuit:
		return true, nil
	case actionNext:
		b.next(filtered)
	case actionPrevious:
		b.previous()
	case actionSearch:
		return false, b.updateSearch()
	case actionUninstall:
		return false, b.uninstall(filtered)
	default:
		b.showDetails(filtered, input)
	}
	return false, nil
}

func (b *packageBrowser) next(filtered []*core.PackageInfo) {
	if b.offset+defaultPageSize < len(filtered) {
		b.offset += defaultPageSize
	}
}

func (b *packageBrowser) previous() {
	b.offset -= defaultPageSize
	if b.offset < 0 {
		b.offset = 0
	}
}

func (b *packageBrowser) updateSearch() error {
	search, err := b.prompt.Input("Search")
	if err != nil {
		return err
	}
	b.search = search
	b.offset = 0
	return nil
}

func (b *packageBrowser) uninstall(filtered []*core.PackageInfo) error {
	if !b.allowUninstall {
		return nil
	}
	selection, err := b.prompt.Input("Package number")
	if err != nil {
		return err
	}
	pkg, ok := selectedPackage(filtered, b.offset, selection)
	if !ok {
		return nil
	}
	if err := confirmAndUninstall(b.reader, pkg); err != nil {
		cliOutput().Status(dx.Error, err.Error())
		return nil
	}
	return b.reloadPackages()
}

func selectedPackage(packages []*core.PackageInfo, offset int, selection string) (*core.PackageInfo, bool) {
	pkg, err := packageBySelection(packages, offset, selection)
	if err != nil {
		cliOutput().Status(dx.Error, err.Error())
		return nil, false
	}
	return pkg, true
}

func (b *packageBrowser) reloadPackages() error {
	packages, err := loadFilteredPackages(packageListOptions{})
	if err != nil {
		return err
	}
	b.packages = packages
	return nil
}

func (b *packageBrowser) showDetails(filtered []*core.PackageInfo, input string) {
	pkg, ok := selectedPackage(filtered, b.offset, input)
	if !ok {
		return
	}
	printPackageDetail(pkg)
}

// printBrowserScreen prints the browser screen
func printBrowserScreen(packages []*core.PackageInfo, offset int, search string, allowUninstall bool) {
	out := cliOutput()
	out.UILine()
	out.UILine(out.UIStyle(dx.Accent, "DIU Packages"))
	if search != "" {
		out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Search:"), search)
	}
	printBrowserPackages(out, packages, offset)
	printBrowserActions(out, allowUninstall)
}

func printBrowserPackages(out *dx.Out, packages []*core.PackageInfo, offset int) {
	if len(packages) == 0 {
		out.UILine(out.UIStyle(dx.Info, "No packages found"))
		return
	}
	end := offset + defaultPageSize
	if end > len(packages) {
		end = len(packages)
	}
	for _, row := range packageRows(packages[offset:end], offset) {
		out.UILine(row)
	}
}

func printBrowserActions(out *dx.Out, allowUninstall bool) {
	actions := "[number] details  / search  n next  p previous  q quit"
	if allowUninstall {
		actions = "[number] details  u uninstall  / search  n next  p previous  q quit"
	}
	out.UILine(out.UIStyle(dx.Muted, actions))
}

// printPackageRows prints package rows with numbering
func printPackageRows(packages []*core.PackageInfo, offset int) {
	out := cliOutput()
	for _, row := range packageRows(packages, offset) {
		out.Println(row)
	}
}

func packageRows(packages []*core.PackageInfo, offset int) []string {
	rows := make([]string, 0, len(packages))
	lastSelection := strconv.Itoa(offset + len(packages))
	indexWidth := max(packageIndexColumnWidth, len(lastSelection))
	widths := []int{indexWidth, packageToolColumnWidth, packageNameColumnWidth, 9, 10}
	for index, pkg := range packages {
		row := dx.Row(widths,
			strconv.Itoa(offset+index+1),
			pkg.Tool,
			pkg.Name,
			fmt.Sprintf("%d uses", pkg.UsageCount),
			formatLastUsed(pkg.LastUsed),
		)
		rows = append(rows, row)
	}
	return rows
}

// printPackageDetail prints detailed information about a package
func printPackageDetail(pkg *core.PackageInfo) {
	out := cliOutput()
	out.UILine()
	out.UILine(out.UIStyle(dx.Accent, pkg.Name))
	out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Tool:"), pkg.Tool)
	out.UIPrintf("%s %d\n", out.UIStyle(dx.Muted, "Used:"), pkg.UsageCount)
	out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Last:"), formatLastUsed(pkg.LastUsed))
	if pkg.Version != "" {
		out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Version:"), pkg.Version)
	}
	if pkg.Path != "" {
		out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Path:"), pkg.Path)
	}
}

// packageBySelection returns the package at the given selection index
func packageBySelection(packages []*core.PackageInfo, offset int, input string) (*core.PackageInfo, error) {
	selection, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil {
		return nil, fmt.Errorf("invalid selection: %s", input)
	}
	index := selection - 1
	if index < 0 {
		return nil, fmt.Errorf("selection out of range: %d", selection)
	}
	if index >= len(packages) {
		return nil, fmt.Errorf("selection out of range: %d", selection)
	}
	return packages[index], nil
}

// confirmAndUninstall confirms and uninstalls a package
func confirmAndUninstall(reader *bufio.Reader, pkg *core.PackageInfo) error {
	if !supportsUninstall(pkg) {
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
	prompt := dx.NewPrompter(reader, cliOutput().Stderr())
	message := fmt.Sprintf("Type %s to uninstall %s", pkg.Name, pkg.Name)
	if err := prompt.Require(message, pkg.Name); err != nil {
		if errors.Is(err, dx.ErrCancelled) {
			return errors.New("uninstall cancelled")
		}
		return fmt.Errorf("failed to read uninstall confirmation: %w", err)
	}
	assumeYes := false
	return uninstallPackage(pkg, assumeYes)
}

// uninstallByName uninstalls a package by name
func uninstallByName(name, tool string, assumeYes bool, dryRun bool) error {
	if err := validateNamedUninstall(assumeYes, dryRun); err != nil {
		return err
	}

	pkg, err := findNamedPackage(name, tool)
	if err != nil {
		return err
	}
	if dryRun {
		return printDryRunUninstall(pkg)
	}

	confirmed := true
	return uninstallPackage(pkg, confirmed)
}

func validateNamedUninstall(assumeYes bool, dryRun bool) error {
	canSkipPrompt := assumeYes || dryRun
	if canSkipPrompt {
		return nil
	}
	return fmt.Errorf("--yes is required when bypassing interactive uninstall")
}

func findNamedPackage(name, tool string) (*core.PackageInfo, error) {
	packages, err := loadFilteredPackages(packageListOptions{
		Tool:   tool,
		Search: name,
		Limit:  0,
	})
	if err != nil {
		return nil, err
	}
	return singlePackageMatch(packages, name)
}

func singlePackageMatch(packages []*core.PackageInfo, name string) (*core.PackageInfo, error) {
	matches := exactPackageMatches(packages, name)
	if len(matches) == 0 {
		return nil, fmt.Errorf("package not found: %s", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple packages match %s; pass --tool", name)
	}
	return matches[0], nil
}

func printDryRunUninstall(pkg *core.PackageInfo) error {
	plan, err := uninstallPlan(pkg)
	if err != nil {
		return err
	}
	cliOutput().Println(strings.Join(printableUninstallPlan(pkg, plan), " "))
	return nil
}

// exactPackageMatches returns packages with exact name match
func exactPackageMatches(packages []*core.PackageInfo, name string) []*core.PackageInfo {
	normalized := strings.ToLower(strings.TrimSpace(name))
	var matches []*core.PackageInfo
	for _, pkg := range packages {
		if strings.ToLower(pkg.Name) == normalized {
			matches = append(matches, pkg)
		}
	}
	return matches
}

// uninstallPackage uninstalls a package
func uninstallPackage(pkg *core.PackageInfo, assumeYes bool) error {
	if !assumeYes {
		if !supportsUninstall(pkg) {
			return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
		}
	}

	if err := runUninstall(pkg); err != nil {
		return err
	}

	if err := removeUninstalledPackageState(pkg); err != nil {
		return err
	}

	cliOutput().Status(dx.Success, pkg.Name+" uninstalled")
	return nil
}

// runUninstall runs the uninstall command for a package
func runUninstall(pkg *core.PackageInfo) error {
	plan, err := uninstallPlan(pkg)
	if err != nil {
		return err
	}

	if removesGoBinary(plan) {
		return removeGoBinary(pkg)
	}
	return runPackageManagerUninstall(pkg)
}

func runPackageManagerUninstall(pkg *core.PackageInfo) error {
	switch pkg.Tool {
	case core.ToolHomebrew, homebrewCaskTool:
		return runHomebrewPackageUninstall(pkg)
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		return runJavaScriptPackageUninstall(pkg)
	case core.ToolPip, core.ToolUV:
		return runPythonPackageUninstall(pkg)
	default:
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

func runHomebrewPackageUninstall(pkg *core.PackageInfo) error {
	isCask := pkg.Tool == homebrewCaskTool
	return runHomebrewUninstall(pkg.Name, isCask)
}

func runJavaScriptPackageUninstall(pkg *core.PackageInfo) error {
	switch pkg.Tool {
	case core.ToolNPM:
		return runNPMUninstall(pkg.Name)
	case core.ToolPNPM:
		return runPNPMUninstall(pkg.Name)
	default:
		return runBunUninstall(pkg.Name)
	}
}

func runPythonPackageUninstall(pkg *core.PackageInfo) error {
	if pkg.Tool == core.ToolPip {
		return runPipUninstall(pkg.Name)
	}
	return runUVUninstall(pkg.Name)
}

func removesGoBinary(plan []string) bool {
	if len(plan) != 1 {
		return false
	}
	return plan[0] == removeFilePlan
}

// removeUninstalledPackageState removes package state from storage
func removeUninstalledPackageState(pkg *core.PackageInfo) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return nil
	}
	if err := removePackageWrapper(config, pkg); err != nil {
		return err
	}
	return deleteUninstalledPackage(config, pkg)
}

func removePackageWrapper(config *core.Config, pkg *core.PackageInfo) error {
	wrapperName := wrapperNameForPackage(pkg)
	if wrapperName == "" {
		return nil
	}
	wrapperDir := config.Monitoring.Process.WrapperDir
	wrapperPath, err := executableWrapperPath(wrapperDir, wrapperName)
	if err != nil {
		return nil
	}
	removeErr := os.Remove(wrapperPath)
	if removeErr == nil {
		return nil
	}
	if os.IsNotExist(removeErr) {
		return nil
	}
	return fmt.Errorf("failed to remove wrapper %s: %w", wrapperPath, removeErr)
}

func deleteUninstalledPackage(config *core.Config, pkg *core.PackageInfo) error {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil
	}
	deleteErr := store.DeletePackage(pkg.Tool, pkg.Name)
	closeErr := store.Close()
	if deleteErr != nil {
		return deletePackageStateError(deleteErr, closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close storage: %w", closeErr)
	}
	return nil
}

func deletePackageStateError(deleteErr error, closeErr error) error {
	if closeErr != nil {
		return fmt.Errorf("failed to delete package state: %w; additionally failed to close storage: %v", deleteErr, closeErr)
	}
	return fmt.Errorf("failed to delete package state: %w", deleteErr)
}
