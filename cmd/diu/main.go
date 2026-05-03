package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

var (
	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86"))
)

const (
	defaultListLimit = 20
	defaultPageSize  = 12

	formatTable = "table"
	formatJSON  = "json"
	formatCSV   = "csv"

	homebrewCommandName = "brew"
	npmCommandName      = "npm"

	homebrewCaskTool = "homebrew-cask"
	homebrewCaskFlag = "--cask"
	npmGlobalFlag    = "-g"

	configSubcommand    = "config"
	getSubcommand       = "get"
	npmPrefixConfigName = "prefix"
	uninstallSubcommand = "uninstall"

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

func main() {
	rootCmd := &cobra.Command{
		Use:   "diu",
		Short: "Do I Use - Package Manager Execution Tracker",
		Long:  `DIU tracks when package managers and global development tools are executed, storing execution data for analysis and auditing.`,
	}

	// Daemon commands
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the DIU daemon",
	}

	daemonStartCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the DIU daemon",
		RunE:  startDaemon,
	}

	daemonStopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the DIU daemon",
		RunE:  stopDaemon,
	}

	daemonRestartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the DIU daemon",
		RunE:  restartDaemon,
	}

	daemonStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check daemon status",
		RunE:  daemonStatus,
	}

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonRestartCmd, daemonStatusCmd)

	// Query command
	var (
		queryTool    string
		queryPackage string
		queryLast    string
		queryLimit   int
		queryFormat  string
	)

	queryCmd := &cobra.Command{
		Use:   "query",
		Short: "Query execution history",
		RunE:  queryExecutions,
	}
	queryCmd.Flags().StringVarP(&queryTool, "tool", "t", "", "Filter by tool (brew, npm, go, etc.)")
	queryCmd.Flags().StringVarP(&queryPackage, "package", "p", "", "Filter by package name")
	queryCmd.Flags().StringVarP(&queryLast, "last", "l", "", "Show executions in last duration (e.g., 24h, 7d)")
	queryCmd.Flags().IntVarP(&queryLimit, "limit", "n", 20, "Limit number of results")
	queryCmd.Flags().StringVarP(&queryFormat, "format", "f", "table", "Output format (table, json, csv)")

	// Stats command
	var (
		statsDaily  bool
		statsWeekly bool
		statsTool   string
		statsTop    int
	)

	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show usage statistics",
		RunE:  showStats,
	}
	statsCmd.Flags().BoolVarP(&statsDaily, "daily", "d", false, "Show daily statistics")
	statsCmd.Flags().BoolVarP(&statsWeekly, "weekly", "w", false, "Show weekly statistics")
	statsCmd.Flags().StringVarP(&statsTool, "tool", "t", "", "Statistics for specific tool")
	statsCmd.Flags().IntVar(&statsTop, "top", 10, "Show top N most used packages")

	// Packages command
	var (
		packagesTool   string
		packagesUnused string
	)

	packagesCmd := &cobra.Command{
		Use:   "packages",
		Short: "List tracked packages",
		RunE:  listPackages,
	}
	packagesCmd.Flags().StringVarP(&packagesTool, "tool", "t", "", "Filter by tool")
	packagesCmd.Flags().StringVarP(&packagesUnused, "unused", "u", "", "Show packages not used in duration")

	var (
		checkTool   string
		checkSearch string
		checkUnused string
		checkLimit  int
		checkFormat string
	)

	checkCmd := &cobra.Command{
		Use:   "check [search]",
		Short: "Check installed package usage",
		RunE:  checkPackages,
	}
	checkCmd.Flags().StringVarP(&checkTool, "tool", "t", "", "Filter by tool")
	checkCmd.Flags().StringVarP(&checkSearch, "search", "s", "", "Search package names")
	checkCmd.Flags().StringVarP(&checkUnused, "unused", "u", "", "Show packages not used in duration")
	checkCmd.Flags().IntVarP(&checkLimit, "limit", "n", defaultListLimit, "Limit non-interactive results")
	checkCmd.Flags().StringVarP(&checkFormat, "format", "f", formatTable, "Output format (table, json, csv)")

	var (
		manageTool      string
		manageSearch    string
		manageUninstall string
		manageYes       bool
		manageDryRun    bool
	)

	manageCmd := &cobra.Command{
		Use:   "manage [search]",
		Short: "Search and uninstall installed packages",
		RunE:  managePackages,
	}
	manageCmd.Flags().StringVarP(&manageTool, "tool", "t", "", "Filter by tool")
	manageCmd.Flags().StringVarP(&manageSearch, "search", "s", "", "Search package names")
	manageCmd.Flags().StringVar(&manageUninstall, "uninstall", "", "Uninstall package non-interactively")
	manageCmd.Flags().BoolVarP(&manageYes, "yes", "y", false, "Skip uninstall confirmation")
	manageCmd.Flags().BoolVar(&manageDryRun, "dry-run", false, "Print uninstall command without running it")

	// Config command
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configGetCmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get configuration value",
		RunE:  getConfig,
	}

	configSetCmd := &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Set configuration value",
		RunE:  setConfig,
	}

	configListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all configuration",
		RunE:  listConfig,
	}

	configCmd.AddCommand(configGetCmd, configSetCmd, configListCmd)

	// Maintenance commands
	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean old executions based on retention",
		RunE:  cleanup,
	}

	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Create manual backup",
		RunE:  backup,
	}

	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Install wrappers and initialize local storage",
		RunE:  setupProject,
	}

	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan installed packages into inventory",
		RunE:  scanPackages,
	}

	recordCmd := &cobra.Command{
		Use:    "record",
		Short:  "Record an execution event from stdin",
		Hidden: true,
		RunE:   recordExecution,
	}

	// Add all commands to root
	rootCmd.AddCommand(
		daemonCmd,
		queryCmd,
		statsCmd,
		packagesCmd,
		checkCmd,
		manageCmd,
		configCmd,
		cleanupCmd,
		backupCmd,
		setupCmd,
		scanCmd,
		recordCmd,
	)

	// Execute with Fang styling
	ctx := context.Background()
	if err := fang.Execute(ctx, rootCmd,
		fang.WithVersion("0.1.0"),
		fang.WithColorSchemeFunc(fang.DefaultColorScheme),
	); err != nil {
		os.Exit(1)
	}
}

func startDaemon(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Check if already running
	if isRunning(config) {
		fmt.Println(infoStyle.Render("DIU daemon is already running"))
		return nil
	}

	d, err := daemon.NewDaemon(config)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	fmt.Println(successStyle.Render("Starting DIU daemon..."))

	// Fork to background
	if os.Getenv("DIU_DAEMON_FOREGROUND") == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		execPath, err = validateExecutablePath(execPath)
		if err != nil {
			return fmt.Errorf("invalid daemon executable path: %w", err)
		}

		args := []string{execPath, "daemon", "start"}
		env := append(os.Environ(), "DIU_DAEMON_FOREGROUND=1")
		devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
		}
		defer func() {
			if err := devNull.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to close %s: %v\n", os.DevNull, err)
			}
		}()

		procAttr := &syscall.ProcAttr{
			Env:   env,
			Files: []uintptr{devNull.Fd(), devNull.Fd(), devNull.Fd()},
			Sys: &syscall.SysProcAttr{
				Setsid: true,
			},
		}

		// #nosec G204 -- execPath is the current executable path and is validated before forking.
		_, err = syscall.ForkExec(execPath, args, procAttr)
		if err != nil {
			return fmt.Errorf("failed to fork daemon: %w", err)
		}

		time.Sleep(time.Second)
		fmt.Println(successStyle.Render("✓ DIU daemon started"))
		return nil
	}

	if err := d.Start(); err != nil {
		return err
	}
	d.Wait()
	return nil
}

func stopDaemon(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !isRunning(config) {
		fmt.Println(infoStyle.Render("DIU daemon is not running"))
		return nil
	}

	pidBytes, err := os.ReadFile(config.Daemon.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return fmt.Errorf("invalid PID: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	fmt.Println(successStyle.Render("✓ DIU daemon stopped"))
	return nil
}

func restartDaemon(cmd *cobra.Command, args []string) error {
	if err := stopDaemon(cmd, args); err != nil {
		return err
	}
	time.Sleep(time.Second)
	return startDaemon(cmd, args)
}

func daemonStatus(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if isRunning(config) {
		fmt.Println(successStyle.Render("✓ DIU daemon is running"))

		pidBytes, _ := os.ReadFile(config.Daemon.PIDFile)
		pid := strings.TrimSpace(string(pidBytes))
		fmt.Println(subtitleStyle.Render("  PID:"), pid)
	} else {
		fmt.Println(errorStyle.Render("✗ DIU daemon is not running"))
	}

	return nil
}

func queryExecutions(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	opts := storage.QueryOptions{
		Tool:    core.NormalizeToolName(cmd.Flag("tool").Value.String()),
		Package: cmd.Flag("package").Value.String(),
	}

	limit, _ := cmd.Flags().GetInt("limit")
	opts.Limit = limit

	if lastStr, _ := cmd.Flags().GetString("last"); lastStr != "" {
		duration, err := parseDuration(lastStr)
		if err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}
		since := time.Now().Add(-duration)
		opts.Since = &since
	}

	executions, err := store.GetExecutions(opts)
	if err != nil {
		return fmt.Errorf("failed to query executions: %w", err)
	}

	format, _ := cmd.Flags().GetString("format")
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(executions)

	case "csv":
		fmt.Println("tool,command,timestamp,duration_ms,exit_code")
		for _, exec := range executions {
			fmt.Printf("%s,%s,%s,%d,%d\n",
				exec.Tool,
				exec.Command,
				exec.Timestamp.Format(time.RFC3339),
				exec.Duration.Milliseconds(),
				exec.ExitCode,
			)
		}

	default: // table
		if len(executions) == 0 {
			fmt.Println(infoStyle.Render("No executions found"))
			return nil
		}

		fmt.Println(titleStyle.Render("Execution History"))
		fmt.Println()

		for _, exec := range executions {
			toolColor := getToolColor(exec.Tool)
			toolStyle := lipgloss.NewStyle().Foreground(toolColor)

			fmt.Printf("%s %s %s\n",
				exec.Timestamp.Format("2006-01-02 15:04:05"),
				toolStyle.Render(fmt.Sprintf("[%s]", exec.Tool)),
				exec.Command,
			)

			if len(exec.PackagesAffected) > 0 {
				fmt.Printf("  %s %s\n",
					subtitleStyle.Render("Packages:"),
					strings.Join(exec.PackagesAffected, ", "),
				)
			}

			if exec.ExitCode != 0 {
				fmt.Printf("  %s %d\n",
					errorStyle.Render("Exit code:"),
					exec.ExitCode,
				)
			}
			fmt.Println()
		}
	}

	return nil
}

func showStats(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	daily, _ := cmd.Flags().GetBool("daily")
	weekly, _ := cmd.Flags().GetBool("weekly")
	toolFilter, _ := cmd.Flags().GetString("tool")

	opts := storage.QueryOptions{}
	if toolFilter != "" {
		opts.Tool = core.NormalizeToolName(toolFilter)
	}

	if daily {
		since := time.Now().Add(-24 * time.Hour)
		opts.Since = &since
		fmt.Println(titleStyle.Render("DIU Statistics (Last 24 Hours)"))
	} else if weekly {
		since := time.Now().Add(-7 * 24 * time.Hour)
		opts.Since = &since
		fmt.Println(titleStyle.Render("DIU Statistics (Last 7 Days)"))
	} else {
		fmt.Println(titleStyle.Render("DIU Statistics"))
	}
	fmt.Println()

	executions, err := store.GetExecutions(opts)
	if err != nil {
		return fmt.Errorf("failed to get executions: %w", err)
	}

	toolCounts := make(map[string]int)
	for _, exec := range executions {
		toolCounts[exec.Tool]++
	}

	fmt.Printf("%s %d\n",
		infoStyle.Render("Total executions:"),
		len(executions),
	)

	stats, _ := store.GetStatistics()
	if stats.MostActiveDay != "" && !daily && !weekly {
		fmt.Printf("%s %s\n",
			infoStyle.Render("Most active day:"),
			stats.MostActiveDay,
		)
	}

	fmt.Println()
	fmt.Println(subtitleStyle.Render("Tool usage:"))
	for tool, count := range toolCounts {
		toolColor := getToolColor(tool)
		toolStyle := lipgloss.NewStyle().Foreground(toolColor)
		fmt.Printf("  %s %d\n", toolStyle.Render(tool+":"), count)
	}

	top, _ := cmd.Flags().GetInt("top")
	if top > 0 {
		packages, _ := store.GetPackages(core.NormalizeToolName(toolFilter))
		sort.Slice(packages, func(i, j int) bool {
			if packages[i].UsageCount == packages[j].UsageCount {
				return packages[i].Name < packages[j].Name
			}
			return packages[i].UsageCount > packages[j].UsageCount
		})
		fmt.Println()
		fmt.Printf(subtitleStyle.Render("Top %d packages:\n"), top)

		for i, pkg := range packages {
			if i >= top {
				break
			}
			fmt.Printf("  %d. %s (%s) - used %d times\n",
				i+1,
				pkg.Name,
				pkg.Tool,
				pkg.UsageCount,
			)
		}
	}

	return nil
}

func listPackages(cmd *cobra.Command, args []string) error {
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
			fmt.Println(successStyle.Render("✓ No unused packages found"))
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
			toolStyle := lipgloss.NewStyle().Bold(true).Foreground(toolColor)
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

type packageListOptions struct {
	Tool   string
	Search string
	Unused string
	Limit  int
	Format string
}

func checkPackages(cmd *cobra.Command, args []string) error {
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

func managePackages(cmd *cobra.Command, args []string) error {
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

func shouldUseInteractive(cmd *cobra.Command, args []string) bool {
	if len(args) > 0 || !isTerminal() {
		return false
	}
	used := false
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		used = true
	})
	return !used
}

func flagString(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func flagInt(cmd *cobra.Command, name string) int {
	value, _ := cmd.Flags().GetInt(name)
	return value
}

func flagBool(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

func closeStore(store storage.Storage) {
	if err := store.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close storage: %v\n", err)
	}
}

func isTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

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

func packageMatchesSearch(pkg *core.PackageInfo, search string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		pkg.Name,
		pkg.Tool,
		pkg.Version,
		pkg.Path,
	}, " "))
	return strings.Contains(haystack, search)
}

func packageUnusedSince(pkg *core.PackageInfo, cutoff time.Time) bool {
	return pkg.LastUsed.IsZero() || pkg.LastUsed.Before(cutoff)
}

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

func printPackageList(packages []*core.PackageInfo, format string) error {
	switch format {
	case formatJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(packages)
	case formatCSV:
		fmt.Println("tool,name,version,usage_count,last_used,path")
		for _, pkg := range packages {
			fmt.Printf("%s,%s,%s,%d,%s,%s\n",
				pkg.Tool,
				pkg.Name,
				pkg.Version,
				pkg.UsageCount,
				formatLastUsed(pkg.LastUsed),
				pkg.Path,
			)
		}
	default:
		if len(packages) == 0 {
			fmt.Println(infoStyle.Render("No packages found"))
			return nil
		}
		printPackageRows(packages, len(packages))
	}
	return nil
}

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

func readPrompt(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

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

	fmt.Printf("%s %s uninstalled\n", successStyle.Render("✓"), pkg.Name)
	return nil
}

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
	default:
		return fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

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
	case core.ToolGo, core.ToolGoBinary:
		if pkg.Path == "" {
			return nil, fmt.Errorf("go package %s has no executable path to remove", pkg.Name)
		}
		return []string{removeFilePlan}, nil
	default:
		return nil, fmt.Errorf("uninstall is not supported for %s packages", pkg.Tool)
	}
}

func printableUninstallPlan(pkg *core.PackageInfo, plan []string) []string {
	if len(plan) == 1 && plan[0] == removeFilePlan {
		return []string{"rm", pkg.Path}
	}
	return plan
}

func supportsUninstall(pkg *core.PackageInfo) bool {
	switch pkg.Tool {
	case core.ToolHomebrew, homebrewCaskTool, core.ToolNPM, core.ToolGo, core.ToolGoBinary:
		return true
	default:
		return false
	}
}

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

func runNPMUninstall(name string) error {
	if err := validatePackageManagerName(name); err != nil {
		return err
	}

	// #nosec G204 -- command is allowlisted and package name is validated before execution.
	command := exec.Command(npmCommandName, uninstallSubcommand, npmGlobalFlag, name)
	return runPreparedCommand(command)
}

func runPreparedCommand(command *exec.Cmd) error {
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		return fmt.Errorf("uninstall failed: %w", err)
	}
	return nil
}

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

func wrapperNameForPackage(pkg *core.PackageInfo) string {
	if pkg.Path != "" {
		return filepath.Base(pkg.Path)
	}
	return pkg.Name
}

func formatLastUsed(lastUsed time.Time) string {
	if lastUsed.IsZero() {
		return "never"
	}
	return lastUsed.Format("2006-01-02")
}

func truncate(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 1 {
		return value[:maxLength]
	}
	return value[:maxLength-1] + "."
}

func getConfig(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("config key required")
	}

	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	key := args[0]
	switch key {
	case "storage.json_file":
		fmt.Println(config.Storage.JSONFile)
	case "storage.retention_days":
		fmt.Println(config.Storage.RetentionDays)
	case "daemon.pid_file":
		fmt.Println(config.Daemon.PIDFile)
	case "api.enabled":
		fmt.Println(config.API.Enabled)
	case "api.port":
		fmt.Println(config.API.Port)
	case "monitoring.enabled_tools":
		fmt.Println(strings.Join(config.Monitoring.EnabledTools, ", "))
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	return nil
}

func setConfig(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("config key and value required")
	}

	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	key := args[0]
	value := args[1]

	switch key {
	case "storage.json_file":
		config.Storage.JSONFile = value
	case "storage.retention_days":
		days, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid retention_days value: %w", err)
		}
		config.Storage.RetentionDays = days
	case "daemon.pid_file":
		config.Daemon.PIDFile = value
	case "api.enabled":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		config.API.Enabled = enabled
	case "api.port":
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value: %w", err)
		}
		config.API.Port = port
	case "monitoring.enabled_tools":
		config.Monitoring.EnabledTools = strings.Split(value, ",")
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println(successStyle.Render("✓ Configuration updated"))
	return nil
}

func listConfig(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(config)
}

func cleanup(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	before := time.Now().AddDate(0, 0, -config.Storage.RetentionDays)
	if err := store.Cleanup(before); err != nil {
		return fmt.Errorf("cleanup failed: %w", err)
	}

	fmt.Println(successStyle.Render("✓ Cleanup completed"))
	return nil
}

func backup(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.Backup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Println(successStyle.Render("✓ Backup created"))
	return nil
}

func setupProject(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirectories(); err != nil {
		return err
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to initialize storage: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("failed to close storage: %w", err)
	}

	if err := installWrappers(config); err != nil {
		return err
	}
	if err := installExecutableWrappers(config); err != nil {
		return err
	}

	fmt.Println(successStyle.Render("✓ DIU setup completed"))
	return nil
}

func scanPackages(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	scanConfig := *config
	scanConfig.Monitoring.Process.AutoInstallWrappers = false

	total := 0
	for _, tool := range scanConfig.Monitoring.EnabledTools {
		monitor, err := newMonitor(core.NormalizeToolName(tool))
		if err != nil {
			continue
		}
		if err := monitor.Initialize(&scanConfig); err != nil {
			fmt.Printf("Warning: failed to initialize %s monitor: %v\n", tool, err)
			continue
		}

		packages, err := monitor.GetInstalledPackages()
		if err != nil {
			fmt.Printf("Warning: failed to scan %s packages: %v\n", tool, err)
			continue
		}

		for _, pkg := range packages {
			if existing, err := store.GetPackage(pkg.Tool, pkg.Name); err == nil {
				pkg.LastUsed = existing.LastUsed
				pkg.UsageCount = existing.UsageCount
			}
			if err := store.UpdatePackage(pkg); err != nil {
				return fmt.Errorf("failed to update package %s/%s: %w", pkg.Tool, pkg.Name, err)
			}
			total++
		}
	}
	seenExecutables := make(map[string]bool)
	for _, target := range discoverExecutableWrappers(config) {
		key := target.Tool + "/" + target.Package
		if seenExecutables[key] || target.Package == "" {
			continue
		}
		seenExecutables[key] = true

		pkg := &core.PackageInfo{
			Name:        target.Package,
			Tool:        target.Tool,
			InstallDate: time.Now(),
			Path:        target.OriginalPath,
		}
		if existing, err := store.GetPackage(pkg.Tool, pkg.Name); err == nil {
			pkg.Version = existing.Version
			pkg.InstallDate = existing.InstallDate
			pkg.LastUsed = existing.LastUsed
			pkg.UsageCount = existing.UsageCount
			if existing.Path != "" {
				pkg.Path = existing.Path
			}
		}
		if err := store.UpdatePackage(pkg); err != nil {
			return fmt.Errorf("failed to update executable package %s/%s: %w", pkg.Tool, pkg.Name, err)
		}
		total++
	}

	fmt.Printf("%s %d packages scanned\n", successStyle.Render("✓"), total)
	return nil
}

func recordExecution(cmd *cobra.Command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	var record core.ExecutionRecord
	if err := json.NewDecoder(os.Stdin).Decode(&record); err != nil {
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

func installWrappers(config *core.Config) error {
	for _, tool := range config.Monitoring.EnabledTools {
		monitor, err := newMonitor(core.NormalizeToolName(tool))
		if err != nil {
			continue
		}
		if err := monitor.Initialize(config); err != nil {
			fmt.Printf("Warning: failed to install %s wrapper: %v\n", tool, err)
		}
	}
	return nil
}

type executableWrapper struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

func installExecutableWrappers(config *core.Config) error {
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

	for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolHomebrew] {
		addExecutableDir(core.ToolHomebrew, dir)
	}
	if npmBin := npmGlobalBinDir(); npmBin != "" {
		addExecutableDir(core.ToolNPM, npmBin)
	}
	if goBin := goBinaryDir(config); goBin != "" {
		addExecutableDir(core.ToolGo, goBin)
	}

	results := make([]executableWrapper, 0, len(targets))
	for _, target := range targets {
		results = append(results, target)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}

func shouldSkipExecutableWrapper(name string) bool {
	switch name {
	case "", ".", "..", "diu", "brew", core.ToolNPM, core.ToolGo:
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

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
	case core.ToolNPM:
		if pkg := npmPackageFromPath(slashPath); pkg != "" {
			return pkg
		}
	}

	return name
}

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

func goBinaryDir(config *core.Config) string {
	if config.Tools.Go.GoBin != "" {
		return config.Tools.Go.GoBin
	}
	if goBin := os.Getenv("GOBIN"); goBin != "" {
		return goBin
	}
	goPath := config.Tools.Go.GoPath
	if goPath == "" {
		goPath = os.Getenv("GOPATH")
	}
	if goPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		goPath = filepath.Join(homeDir, "go")
	}
	return filepath.Join(goPath, "bin")
}

func writeExecutableWrapper(config *core.Config, target executableWrapper) error {
	diuPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve diu executable: %w", err)
	}

	wrapperPath, err := executableWrapperPath(config.Monitoring.Process.WrapperDir, target.Name)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`#!/bin/bash
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

sent=false
if [ -S "$DIU_SOCKET" ] && command -v nc >/dev/null 2>&1; then
    if printf '%%s\n' "$payload" | nc -U "$DIU_SOCKET" 2>/dev/null; then
        sent=true
    fi
fi

if [ "$sent" != true ] && [ -x "$DIU_BINARY" ]; then
    printf '%%s\n' "$payload" | "$DIU_BINARY" record >/dev/null 2>&1 || true
fi

exit $EXIT_CODE
`, core.DefaultSocketPath, diuPath, target.OriginalPath, target.Tool, target.Package, target.Name)

	return writeOwnerExecutableFile(wrapperPath, []byte(script))
}

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

	parsed, err := monitor.ParseCommand(record.Command, record.Args)
	if err != nil {
		return
	}

	if len(record.PackagesAffected) == 0 {
		record.PackagesAffected = parsed.PackagesAffected
	}

	if len(parsed.Metadata) == 0 {
		return
	}
	if record.Metadata == nil {
		record.Metadata = make(map[string]interface{})
	}
	for key, value := range parsed.Metadata {
		if _, exists := record.Metadata[key]; !exists {
			record.Metadata[key] = value
		}
	}
}

func newMonitor(tool string) (monitors.Monitor, error) {
	switch core.NormalizeToolName(tool) {
	case core.ToolHomebrew:
		return monitors.NewHomebrewMonitor(), nil
	case core.ToolNPM:
		return monitors.NewNPMMonitor(), nil
	case core.ToolGo:
		return monitors.NewGoMonitor(), nil
	default:
		return nil, fmt.Errorf("unsupported tool: %s", tool)
	}
}

func isRunning(config *core.Config) bool {
	return daemon.IsRunning(config)
}

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

func getToolColor(tool string) lipgloss.Color {
	switch core.NormalizeToolName(tool) {
	case "homebrew":
		return lipgloss.Color("214") // Orange
	case "npm":
		return lipgloss.Color("196") // Red
	case "go":
		return lipgloss.Color("86") // Cyan
	case "pip", "python":
		return lipgloss.Color("226") // Yellow
	case "gem", "ruby":
		return lipgloss.Color("160") // Red
	case "cargo", "rust":
		return lipgloss.Color("208") // Orange
	default:
		return lipgloss.Color("250") // Gray
	}
}
