package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
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
func listPackages(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	tool, _ := cmd.Flags().GetString("tool")
	tool = core.NormalizeToolName(tool)
	packages, err := store.GetPackages(tool)
	if err != nil {
		return fmt.Errorf("failed to get packages: %w", err)
	}

	if len(packages) == 0 {
		fmt.Println(infoStyle.Render("No packages tracked"))
		return nil
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Tool == packages[j].Tool {
			return packages[i].Name < packages[j].Name
		}
		return packages[i].Tool < packages[j].Tool
	})

	// Filter by unused duration if specified
	if unusedStr, _ := cmd.Flags().GetString("unused"); unusedStr != "" {
		duration, err := parseDuration(unusedStr)
		if err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}

		cutoff := time.Now().Add(-duration)
		var filtered []*core.PackageInfo
		for _, pkg := range packages {
			if pkg.LastUsed.Before(cutoff) {
				filtered = append(filtered, pkg)
			}
		}
		packages = filtered

		if len(packages) == 0 {
			fmt.Println(successStyle.Render("No unused packages found"))
			return nil
		}
	}

	fmt.Println(titleStyle.Render("Tracked Packages"))
	fmt.Println()

	currentTool := ""
	for _, pkg := range packages {
		if pkg.Tool != currentTool {
			currentTool = pkg.Tool
			toolColor := getToolColor(pkg.Tool)
			toolStyle := newStyle().Bold(true).Foreground(toolColor)
			fmt.Println()
			fmt.Println(toolStyle.Render(pkg.Tool))
		}

		fmt.Printf("  %s", pkg.Name)
		if pkg.Version != "" {
			fmt.Printf(" (%s)", pkg.Version)
		}
		lastUsed := "never"
		if !pkg.LastUsed.IsZero() {
			lastUsed = pkg.LastUsed.Format("2006-01-02")
		}
		fmt.Printf(" - used %d times, last: %s\n",
			pkg.UsageCount,
			lastUsed,
		)
	}

	return nil
}

// checkPackages checks installed package usage
func checkPackages(cmd *command, args []string) error {
	opts := packageListOptions{
		Tool:   flagString(cmd, "tool"),
		Search: flagString(cmd, "search"),
		Unused: flagString(cmd, "unused"),
		Limit:  flagInt(cmd, "limit"),
		Format: flagString(cmd, "format"),
	}
	if opts.Search == "" && len(args) > 0 {
		opts.Search = strings.Join(args, " ")
	}

	if shouldUseInteractive(cmd, args) {
		return runPackageBrowser(false)
	}

	packages, err := loadFilteredPackages(opts)
	if err != nil {
		return err
	}
	return printPackageList(packages, opts.Format)
}

// managePackages searches and uninstalls installed packages
func managePackages(cmd *command, args []string) error {
	tool := flagString(cmd, "tool")
	search := flagString(cmd, "search")
	uninstallName := flagString(cmd, "uninstall")
	assumeYes := flagBool(cmd, "yes")
	dryRun := flagBool(cmd, "dry-run")

	if search == "" && uninstallName == "" && len(args) > 0 {
		search = strings.Join(args, " ")
	}
	if uninstallName == "" && len(args) > 0 && assumeYes {
		uninstallName = strings.Join(args, " ")
	}

	if uninstallName != "" {
		return uninstallByName(uninstallName, tool, assumeYes, dryRun)
	}

	if shouldUseInteractive(cmd, args) {
		return runPackageBrowser(true)
	}

	packages, err := loadFilteredPackages(packageListOptions{
		Tool:   tool,
		Search: search,
		Limit:  defaultListLimit,
		Format: formatTable,
	})
	if err != nil {
		return err
	}
	return printPackageList(packages, formatTable)
}

// shouldUseInteractive returns true if the command should use interactive mode
func shouldUseInteractive(cmd *command, args []string) bool {
	if len(args) > 0 || !isTerminal() {
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
	config, err := core.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	packages, err := store.GetPackages(core.NormalizeToolName(opts.Tool))
	if err != nil {
		return nil, fmt.Errorf("failed to get packages: %w", err)
	}

	filtered, err := filterPackages(packages, opts)
	if err != nil {
		return nil, err
	}
	sortPackages(filtered)
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return filtered, nil
}

// filterPackages filters packages by search and unused criteria
func filterPackages(packages []*core.PackageInfo, opts packageListOptions) ([]*core.PackageInfo, error) {
	var cutoff time.Time
	if opts.Unused != "" {
		duration, err := parseDuration(opts.Unused)
		if err != nil {
			return nil, fmt.Errorf("invalid unused duration: %w", err)
		}
		cutoff = time.Now().Add(-duration)
	}

	search := strings.ToLower(strings.TrimSpace(opts.Search))
	var filtered []*core.PackageInfo
	for _, pkg := range packages {
		if search != "" && !packageMatchesSearch(pkg, search) {
			continue
		}
		if !cutoff.IsZero() && !packageUnusedSince(pkg, cutoff) {
			continue
		}
		filtered = append(filtered, pkg)
	}
	return filtered, nil
}

// printPackageList prints a list of packages in the specified format
func printPackageList(packages []*core.PackageInfo, format string) error {
	switch format {
	case formatJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(packages)
	case formatCSV:
		writer := csv.NewWriter(os.Stdout)
		if err := writer.Write([]string{"tool", "name", "version", "usage_count", "last_used", "path"}); err != nil {
			return err
		}
		for _, pkg := range packages {
			if err := writer.Write([]string{
				pkg.Tool,
				pkg.Name,
				pkg.Version,
				strconv.Itoa(pkg.UsageCount),
				formatLastUsed(pkg.LastUsed),
				pkg.Path,
			}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		if len(packages) == 0 {
			fmt.Println(infoStyle.Render("No packages found"))
			return nil
		}
		printPackageRows(packages, 0)
	}
	return nil
}

// runPackageBrowser runs the interactive package browser
func runPackageBrowser(allowUninstall bool) error {
	packages, err := loadFilteredPackages(packageListOptions{})
	if err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	search := ""
	offset := 0

	for {
		filtered, err := filterPackages(packages, packageListOptions{Search: search})
		if err != nil {
			return err
		}
		sortPackages(filtered)
		if offset >= len(filtered) {
			offset = 0
		}

		printBrowserScreen(filtered, offset, search, allowUninstall)
		input, err := readPrompt(reader, "diu> ")
		if err != nil {
			return err
		}

		switch input {
		case actionQuit:
			return nil
		case actionNext:
			if offset+defaultPageSize < len(filtered) {
				offset += defaultPageSize
			}
		case actionPrevious:
			offset -= defaultPageSize
			if offset < 0 {
				offset = 0
			}
		case actionSearch:
			search, err = readPrompt(reader, "search> ")
			if err != nil {
				return err
			}
			offset = 0
		case actionUninstall:
			if !allowUninstall {
				continue
			}
			selection, err := readPrompt(reader, "number> ")
			if err != nil {
				return err
			}
			pkg, err := packageBySelection(filtered, offset, selection)
			if err != nil {
				fmt.Println(errorStyle.Render(err.Error()))
				continue
			}
			if err := confirmAndUninstall(reader, pkg); err != nil {
				fmt.Println(errorStyle.Render(err.Error()))
				continue
			}
			packages, err = loadFilteredPackages(packageListOptions{})
			if err != nil {
				return err
			}
		default:
			pkg, err := packageBySelection(filtered, offset, input)
			if err != nil {
				fmt.Println(errorStyle.Render(err.Error()))
				continue
			}
			printPackageDetail(pkg)
		}
	}
}

// printBrowserScreen prints the browser screen
func printBrowserScreen(packages []*core.PackageInfo, offset int, search string, allowUninstall bool) {
	fmt.Println()
	fmt.Println(titleStyle.Render("DIU Packages"))
	if search != "" {
		fmt.Printf("%s %s\n", subtitleStyle.Render("Search:"), search)
	}
	if len(packages) == 0 {
		fmt.Println(infoStyle.Render("No packages found"))
	} else {
		end := offset + defaultPageSize
		if end > len(packages) {
			end = len(packages)
		}
		printPackageRows(packages[offset:end], offset)
	}
	actions := "[number] details  / search  n next  p previous  q quit"
	if allowUninstall {
		actions = "[number] details  u uninstall  / search  n next  p previous  q quit"
	}
	fmt.Println(subtitleStyle.Render(actions))
}

// printPackageRows prints package rows with numbering
func printPackageRows(packages []*core.PackageInfo, offset int) {
	for index, pkg := range packages {
		fmt.Printf("%*d  %-*s %-*s used %-*d last %s\n",
			packageIndexColumnWidth,
			offset+index+1,
			packageToolColumnWidth,
			pkg.Tool,
			packageNameColumnWidth,
			truncate(pkg.Name, packageNameColumnWidth),
			packageUsageColumnWidth,
			pkg.UsageCount,
			formatLastUsed(pkg.LastUsed),
		)
	}
}

// printPackageDetail prints detailed information about a package
func printPackageDetail(pkg *core.PackageInfo) {
	fmt.Println()
	fmt.Println(titleStyle.Render(pkg.Name))
	fmt.Printf("%s %s\n", subtitleStyle.Render("Tool:"), pkg.Tool)
	fmt.Printf("%s %d\n", subtitleStyle.Render("Used:"), pkg.UsageCount)
	fmt.Printf("%s %s\n", subtitleStyle.Render("Last:"), formatLastUsed(pkg.LastUsed))
	if pkg.Version != "" {
		fmt.Printf("%s %s\n", subtitleStyle.Render("Version:"), pkg.Version)
	}
	if pkg.Path != "" {
		fmt.Printf("%s %s\n", subtitleStyle.Render("Path:"), pkg.Path)
	}
}

// packageBySelection returns the package at the given selection index
func packageBySelection(packages []*core.PackageInfo, offset int, input string) (*core.PackageInfo, error) {
	selection, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil {
		return nil, fmt.Errorf("invalid selection: %s", input)
	}
	index := selection - 1
	if index < 0 || index >= len(packages) {
		return nil, fmt.Errorf("selection out of range: %d", selection)
	}
	return packages[index], nil
}

// confirmAndUninstall confirms and uninstalls a package
func confirmAndUninstall(reader *bufio.Reader, pkg *core.PackageInfo) error {
	if !supportsUninstall(pkg) {
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
	fmt.Printf("Type %s to uninstall %s: ", pkg.Name, pkg.Name)
	confirmation, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirmation) != pkg.Name {
		return fmt.Errorf("uninstall cancelled")
	}
	return uninstallPackage(pkg, false)
}

// uninstallByName uninstalls a package by name
func uninstallByName(name, tool string, assumeYes bool, dryRun bool) error {
	if !assumeYes && !dryRun {
		return fmt.Errorf("--yes is required when bypassing interactive uninstall")
	}

	packages, err := loadFilteredPackages(packageListOptions{
		Tool:   tool,
		Search: name,
		Limit:  0,
	})
	if err != nil {
		return err
	}

	matches := exactPackageMatches(packages, name)
	if len(matches) == 0 {
		return fmt.Errorf("package not found: %s", name)
	}
	if len(matches) > 1 {
		return fmt.Errorf("multiple packages match %s; pass --tool", name)
	}

	if dryRun {
		plan, err := uninstallPlan(matches[0])
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(printableUninstallPlan(matches[0], plan), " "))
		return nil
	}

	return uninstallPackage(matches[0], true)
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
	if !assumeYes && !supportsUninstall(pkg) {
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}

	if err := runUninstall(pkg); err != nil {
		return err
	}

	if err := removeUninstalledPackageState(pkg); err != nil {
		return err
	}

	fmt.Printf("%s\n", successStyle.Render(pkg.Name+" uninstalled"))
	return nil
}

// runUninstall runs the uninstall command for a package
func runUninstall(pkg *core.PackageInfo) error {
	plan, err := uninstallPlan(pkg)
	if err != nil {
		return err
	}

	if len(plan) == 1 && plan[0] == removeFilePlan {
		return removeGoBinary(pkg)
	}

	switch pkg.Tool {
	case core.ToolHomebrew:
		return runHomebrewUninstall(pkg.Name, false)
	case homebrewCaskTool:
		return runHomebrewUninstall(pkg.Name, true)
	case core.ToolNPM:
		return runNPMUninstall(pkg.Name)
	case core.ToolPNPM:
		return runPNPMUninstall(pkg.Name)
	case core.ToolBun:
		return runBunUninstall(pkg.Name)
	case core.ToolPip:
		return runPipUninstall(pkg.Name)
	case core.ToolUV:
		return runUVUninstall(pkg.Name)
	default:
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

// removeUninstalledPackageState removes package state from storage
func removeUninstalledPackageState(pkg *core.PackageInfo) error {
	config, err := core.LoadConfig("")
	if err == nil {
		if wrapperName := wrapperNameForPackage(pkg); wrapperName != "" {
			wrapperPath, pathErr := executableWrapperPath(config.Monitoring.Process.WrapperDir, wrapperName)
			if pathErr == nil {
				if removeErr := os.Remove(wrapperPath); removeErr != nil && !os.IsNotExist(removeErr) {
					return fmt.Errorf("failed to remove wrapper %s: %w", wrapperPath, removeErr)
				}
			}
		}
		if store, err := storage.NewJSONStorage(config); err == nil {
			if deleteErr := store.DeletePackage(pkg.Tool, pkg.Name); deleteErr != nil {
				closeErr := store.Close()
				if closeErr != nil {
					return fmt.Errorf("failed to delete package state: %w; additionally failed to close storage: %v", deleteErr, closeErr)
				}
				return fmt.Errorf("failed to delete package state: %w", deleteErr)
			}
			if closeErr := store.Close(); closeErr != nil {
				return fmt.Errorf("failed to close storage: %w", closeErr)
			}
		}
	}

	return nil
}
