package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
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

	// Add all commands to root
	rootCmd.AddCommand(
		daemonCmd,
		queryCmd,
		statsCmd,
		packagesCmd,
		configCmd,
		cleanupCmd,
		backupCmd,
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

		args := []string{execPath, "daemon", "start"}
		env := append(os.Environ(), "DIU_DAEMON_FOREGROUND=1")

		procAttr := &syscall.ProcAttr{
			Env:   env,
			Files: []uintptr{0, 1, 2},
		}

		_, err = syscall.ForkExec(execPath, args, procAttr)
		if err != nil {
			return fmt.Errorf("failed to fork daemon: %w", err)
		}

		time.Sleep(time.Second)
		fmt.Println(successStyle.Render("✓ DIU daemon started"))
		return nil
	}

	// Run in foreground
	return d.Start()
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
	defer store.Close()

	opts := storage.QueryOptions{
		Tool:    cmd.Flag("tool").Value.String(),
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
	defer store.Close()

	daily, _ := cmd.Flags().GetBool("daily")
	weekly, _ := cmd.Flags().GetBool("weekly")
	toolFilter, _ := cmd.Flags().GetString("tool")

	opts := storage.QueryOptions{}
	if toolFilter != "" {
		opts.Tool = toolFilter
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
		packages, _ := store.GetPackages(toolFilter)
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
	defer store.Close()

	tool, _ := cmd.Flags().GetString("tool")
	packages, err := store.GetPackages(tool)
	if err != nil {
		return fmt.Errorf("failed to get packages: %w", err)
	}

	if len(packages) == 0 {
		fmt.Println(infoStyle.Render("No packages tracked"))
		return nil
	}

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
		fmt.Printf(" - used %d times, last: %s\n",
			pkg.UsageCount,
			pkg.LastUsed.Format("2006-01-02"),
		)
	}

	return nil
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
	defer store.Close()

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
	defer store.Close()

	if err := store.Backup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Println(successStyle.Render("✓ Backup created"))
	return nil
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
	switch tool {
	case "homebrew":
		return lipgloss.Color("214") // Orange
	case "npm":
		return lipgloss.Color("196") // Red
	case "go":
		return lipgloss.Color("86")  // Cyan
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