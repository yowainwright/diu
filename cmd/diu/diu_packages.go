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

type packageListOptions struct {
	Tool   string
	Search string
	Unused string
	Limit  int
	Format string
}

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
	store, err := openStore()
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
	out.Println(out.StyleData(dx.Info, "No packages tracked"))
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
	out.Println(out.StyleData(dx.Success, "No unused packages found"))
}

func printTrackedPackages(packages []*core.PackageInfo) {
	out := cliOutput()
	out.Println(out.StyleData(dx.Accent, "Tracked Packages"))
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
	out.Println(out.StyleData(dx.Accent, pkg.Tool))
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

func checkPackages(cmd *command, args []string) error {
	opts := checkPackageOptions(cmd, args)
	if shouldUseInteractive(cmd, args) {
		canUninstall := false
		return runPackageBrowser(canUninstall)
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

func managePackages(cmd *command, args []string) error {
	opts := managePackageOptions(cmd, args)
	if opts.uninstallName != "" {
		return uninstallByName(opts.uninstallName, opts.tool, opts.shouldAssumeYes, opts.shouldDryRun)
	}

	if shouldUseInteractive(cmd, args) {
		canUninstall := true
		return runPackageBrowser(canUninstall)
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
	tool            string
	search          string
	uninstallName   string
	shouldAssumeYes bool
	shouldDryRun    bool
}

func managePackageOptions(cmd *command, args []string) managePackageOpts {
	opts := managePackageOpts{
		tool:            flagString(cmd, "tool"),
		search:          flagString(cmd, "search"),
		uninstallName:   flagString(cmd, "uninstall"),
		shouldAssumeYes: flagBool(cmd, "yes"),
		shouldDryRun:    flagBool(cmd, "dry-run"),
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
	if opts.shouldAssumeYes {
		opts.uninstallName = strings.Join(args, " ")
	}
}

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

func loadFilteredPackages(opts packageListOptions) ([]*core.PackageInfo, error) {
	store, err := openStore()
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
		out.Println(out.StyleData(dx.Info, "No packages found"))
		return nil
	}
	printPackageRows(packages, 0)
	return nil
}

func runPackageBrowser(canUninstall bool) error {
	packages, err := loadFilteredPackages(packageListOptions{})
	if err != nil {
		return err
	}
	out := cliOutput()
	reader := bufio.NewReader(out.Stdin())
	prompt := dx.NewPrompter(reader, out.Stderr())
	browser := packageBrowser{
		packages:     packages,
		reader:       reader,
		prompt:       prompt,
		canUninstall: canUninstall,
	}
	return browser.run()
}

type packageBrowser struct {
	packages     []*core.PackageInfo
	reader       *bufio.Reader
	prompt       *dx.Prompter
	search       string
	offset       int
	canUninstall bool
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
	printBrowserScreen(filtered, b.offset, b.search, b.canUninstall)
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
	if !b.canUninstall {
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

func printBrowserScreen(packages []*core.PackageInfo, offset int, search string, canUninstall bool) {
	out := cliOutput()
	out.UILine()
	out.UILine(out.UIStyle(dx.Accent, "DIU Packages"))
	if search != "" {
		out.UIPrintf("%s %s\n", out.UIStyle(dx.Muted, "Search:"), search)
	}
	printBrowserPackages(out, packages, offset)
	printBrowserActions(out, canUninstall)
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

func printBrowserActions(out *dx.Out, canUninstall bool) {
	actions := "[number] details  / search  n next  p previous  q quit"
	if canUninstall {
		actions = "[number] details  u uninstall  / search  n next  p previous  q quit"
	}
	out.UILine(out.UIStyle(dx.Muted, actions))
}

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
	shouldAssumeYes := false
	return uninstallPackage(pkg, shouldAssumeYes)
}

func uninstallByName(name, tool string, shouldAssumeYes bool, shouldDryRun bool) error {
	if err := validateNamedUninstall(shouldAssumeYes, shouldDryRun); err != nil {
		return err
	}

	pkg, err := findNamedPackage(name, tool)
	if err != nil {
		return err
	}
	if shouldDryRun {
		return printDryRunUninstall(pkg)
	}

	confirmed := true
	return uninstallPackage(pkg, confirmed)
}

func validateNamedUninstall(shouldAssumeYes bool, shouldDryRun bool) error {
	canSkipPrompt := shouldAssumeYes || shouldDryRun
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

func uninstallPackage(pkg *core.PackageInfo, shouldAssumeYes bool) error {
	if !shouldAssumeYes {
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
