package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/shell"
	"github.com/yowainwright/diu/internal/storage"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	subtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "diu",
		Short: "Do I Use - track globally installed package usage",
		Long:  `diu tracks globally installed packages — what's installed, what version, when last updated, and when last used.`,
	}

	// setup
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Inject shell hooks to track package manager usage",
		RunE:  runSetup,
	}

	// teardown
	teardownCmd := &cobra.Command{
		Use:   "teardown",
		Short: "Remove shell hooks",
		RunE:  runTeardown,
	}

	// status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show hook status and storage info",
		RunE:  runStatus,
	}

	// scan
	var scanTool string
	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan installed packages and update the cache",
		RunE:  runScan,
	}
	scanCmd.Flags().StringVarP(&scanTool, "tool", "t", "", "Scan only this tool (brew, npm, go)")
	_ = scanTool

	// record (hidden — called by shell hooks)
	var (
		recordTool     string
		recordExitCode int
	)
	recordCmd := &cobra.Command{
		Use:    "record",
		Hidden: true,
		Short:  "Record a package manager invocation (called by shell hooks)",
		RunE:   runRecord,
	}
	recordCmd.Flags().StringVar(&recordTool, "tool", "", "Tool name")
	recordCmd.Flags().IntVar(&recordExitCode, "exit-code", 0, "Exit code of the original command")
	_ = recordTool
	_ = recordExitCode

	// list (packages)
	var (
		listTool   string
		listUnused string
		listFormat string
	)
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"packages"},
		Short:   "List tracked packages",
		RunE:    runList,
	}
	listCmd.Flags().StringVarP(&listTool, "tool", "t", "", "Filter by tool")
	listCmd.Flags().StringVarP(&listUnused, "unused", "u", "", "Show packages not used in this duration (e.g. 90d)")
	listCmd.Flags().StringVarP(&listFormat, "format", "f", "table", "Output format: table or json")
	_ = listTool
	_ = listUnused
	_ = listFormat

	// stats
	var (
		statsTool string
		statsTop  int
	)
	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show usage statistics",
		RunE:  runStats,
	}
	statsCmd.Flags().StringVarP(&statsTool, "tool", "t", "", "Filter by tool")
	statsCmd.Flags().IntVar(&statsTop, "top", 10, "Show top N packages by usage count")
	_ = statsTool
	_ = statsTop

	// cleanup
	var cleanupBefore string
	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove old execution records",
		RunE:  runCleanup,
	}
	cleanupCmd.Flags().StringVarP(&cleanupBefore, "before", "b", "", "Remove records older than this duration (e.g. 90d); defaults to retention_days config")
	_ = cleanupBefore

	// config
	configCmd := &cobra.Command{Use: "config", Short: "Manage configuration"}
	configCmd.AddCommand(
		&cobra.Command{Use: "list", Short: "Print current config as JSON", RunE: runConfigList},
		&cobra.Command{Use: "get [key]", Short: "Get a config value", RunE: runConfigGet},
		&cobra.Command{Use: "set [key] [value]", Short: "Set a config value", RunE: runConfigSet},
	)

	rootCmd.AddCommand(
		setupCmd, teardownCmd, statusCmd, scanCmd,
		recordCmd,
		listCmd, statsCmd, cleanupCmd,
		configCmd,
	)

	ctx := context.Background()
	if err := fang.Execute(ctx, rootCmd,
		fang.WithVersion("0.1.0"),
		fang.WithColorSchemeFunc(fang.DefaultColorScheme),
	); err != nil {
		os.Exit(1)
	}
}

// ── setup / teardown / status ──────────────────────────────────────────────

func runSetup(_ *cobra.Command, _ []string) error {
	installed, err := shell.Setup()
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		fmt.Println(infoStyle.Render("Shell hooks already installed — nothing to do"))
		return nil
	}
	for _, f := range installed {
		fmt.Println(successStyle.Render("✓ Hooks added to " + f))
	}
	fmt.Println(subtitleStyle.Render("Restart your shell or run: source ~/.zshrc (or ~/.bashrc)"))
	return nil
}

func runTeardown(_ *cobra.Command, _ []string) error {
	removed, err := shell.Teardown()
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Println(infoStyle.Render("No shell hooks found — nothing to remove"))
		return nil
	}
	for _, f := range removed {
		fmt.Println(successStyle.Render("✓ Hooks removed from " + f))
	}
	return nil
}

func runStatus(_ *cobra.Command, _ []string) error {
	fmt.Println(titleStyle.Render("diu status"))
	fmt.Println()

	// Shell hooks
	fmt.Println(subtitleStyle.Render("Shell hooks:"))
	hookStatus := shell.Status()
	if len(hookStatus) == 0 {
		fmt.Println("  " + infoStyle.Render("No shell config files detected"))
	}
	for file, active := range hookStatus {
		if active {
			fmt.Printf("  %s %s\n", successStyle.Render("✓"), file)
		} else {
			fmt.Printf("  %s %s\n", errorStyle.Render("✗"), file)
		}
	}

	fmt.Println()

	// Storage
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	fmt.Println(subtitleStyle.Render("Storage:"))
	fmt.Printf("  File: %s\n", config.Storage.JSONFile)

	store, err := storage.NewJSONStorage(config)
	if err == nil {
		defer store.Close()
		stats, _ := store.GetStatistics()
		if stats != nil {
			fmt.Printf("  Executions recorded: %d\n", stats.TotalExecutions)
		}
		if pkgs, err := store.GetPackages(""); err == nil {
			fmt.Printf("  Packages tracked: %d\n", len(pkgs))
		}
	}

	return nil
}

// ── scan ───────────────────────────────────────────────────────────────────

func runScan(cmd *cobra.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return err
	}
	defer store.Close()

	filterTool, _ := cmd.Flags().GetString("tool")
	filterTool = normalizeToolName(filterTool)

	type result struct {
		tool    string
		added   int
		updated int
		total   int
		err     error
	}

	var results []result

	monitorDefs := []struct {
		name    string
		factory func() monitors.Monitor
	}{
		{"homebrew", monitors.NewHomebrewMonitor},
		{"npm", monitors.NewNPMMonitor},
		{"go", monitors.NewGoMonitor},
	}

	availableTools := make(map[string]bool, len(monitorDefs))
	for _, def := range monitorDefs {
		availableTools[def.name] = true
	}
	if filterTool != "" && !availableTools[filterTool] {
		return fmt.Errorf("unsupported scan tool %q", filterTool)
	}

	enabledTools := make(map[string]bool, len(config.Monitoring.EnabledTools))
	for _, name := range config.Monitoring.EnabledTools {
		enabledTools[normalizeToolName(strings.TrimSpace(name))] = true
	}

	for _, def := range monitorDefs {
		if filterTool != "" && def.name != filterTool {
			continue
		}
		if filterTool == "" && !enabledTools[def.name] {
			continue
		}

		m := def.factory()
		if err := m.Initialize(config); err != nil {
			results = append(results, result{tool: def.name, err: err})
			continue
		}

		pkgs, err := m.GetInstalledPackages()
		if err != nil {
			results = append(results, result{tool: def.name, err: err})
			continue
		}

		r := result{tool: def.name, total: len(pkgs)}
		now := time.Now()

		for _, pkg := range pkgs {
			existing, getErr := store.GetPackage(pkg.Tool, pkg.Name)
			if getErr != nil {
				// New package
				if pkg.InstallDate.IsZero() {
					pkg.InstallDate = now
				}
				pkg.LastUpdated = now
				if pkg.LastUsed.IsZero() {
					pkg.LastUsed = pkg.InstallDate
				}
				if err2 := store.UpdatePackage(pkg); err2 == nil {
					r.added++
				}
			} else {
				// Update version if changed
				if pkg.Version != "" && pkg.Version != existing.Version {
					existing.Version = pkg.Version
					existing.LastUpdated = now
					r.updated++
				}
				if pkg.Path != "" {
					existing.Path = pkg.Path
				}
				if len(pkg.Dependencies) > 0 {
					existing.Dependencies = pkg.Dependencies
				}
				store.UpdatePackage(existing) //nolint:errcheck
			}
		}

		results = append(results, r)
	}

	fmt.Println(titleStyle.Render("Scan results"))
	fmt.Println()
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  %s %s: %s\n", errorStyle.Render("✗"), r.tool, r.err)
			continue
		}
		line := fmt.Sprintf("%d packages", r.total)
		if r.added > 0 || r.updated > 0 {
			line += fmt.Sprintf(" (%d new, %d updated)", r.added, r.updated)
		}
		fmt.Printf("  %s %s: %s\n", successStyle.Render("✓"), r.tool, line)
	}

	return nil
}

// ── record (called by shell hooks) ────────────────────────────────────────

// installActions maps tool → subcommands that indicate a package install/upgrade.
var installActions = map[string]map[string]bool{
	"homebrew": {"install": true, "upgrade": true, "reinstall": true},
	"npm":      {"install": true, "i": true, "add": true, "update": true, "up": true},
	"go":       {"install": true, "get": true},
	"pip":      {"install": true},
	"pip3":     {"install": true},
	"cargo":    {"install": true},
	"gem":      {"install": true, "update": true},
}

// upgradeActions marks subcommands where LastUpdated should be bumped.
var upgradeActions = map[string]bool{
	"upgrade": true, "update": true, "up": true, "reinstall": true,
}

func normalizeToolName(name string) string {
	switch name {
	case "brew":
		return "homebrew"
	case "homebrew-cask":
		return "homebrew"
	case "pip3":
		return "pip"
	case "go-binary":
		return "go"
	}
	return name
}

func runRecord(cmd *cobra.Command, args []string) error {
	tool, _ := cmd.Flags().GetString("tool")
	exitCode, _ := cmd.Flags().GetInt("exit-code")

	if tool == "" || exitCode != 0 || len(args) == 0 {
		return nil
	}

	normalTool := normalizeToolName(tool)

	actions, ok := installActions[normalTool]
	if !ok {
		return nil
	}
	if !actions[args[0]] {
		return nil
	}

	// For npm, only record global operations.
	if normalTool == "npm" {
		global := false
		for _, a := range args {
			if a == "-g" || a == "--global" {
				global = true
				break
			}
		}
		if !global {
			return nil
		}
	}

	pkgs := extractPackages(normalTool, args)

	config, err := core.LoadConfig("")
	if err != nil {
		return nil
	}
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil
	}
	defer store.Close()

	now := time.Now()
	isUpgrade := upgradeActions[args[0]]

	// Record the execution for stats tracking.
	record := &core.ExecutionRecord{
		Tool:             normalTool,
		Command:          tool + " " + strings.Join(args, " "),
		Args:             args,
		Timestamp:        now,
		ExitCode:         exitCode,
		PackagesAffected: pkgs,
	}
	if err := store.AddExecution(record); err != nil {
		return nil
	}

	if isUpgrade {
		for _, name := range pkgs {
			existing, getErr := store.GetPackage(normalTool, name)
			if getErr != nil {
				continue
			}
			existing.LastUpdated = now
			store.UpdatePackage(existing) //nolint:errcheck
		}
	}

	return nil
}

// extractPackages pulls package names out of a tool's arg list, skipping flags.
func extractPackages(tool string, args []string) []string {
	if len(args) == 0 {
		return nil
	}

	// Use the homebrew monitor's parse logic for brew.
	if tool == "homebrew" {
		m := monitors.NewHomebrewMonitor().(*monitors.HomebrewMonitor)
		record, err := m.ParseCommand("brew", args)
		if err == nil {
			return record.PackagesAffected
		}
	}

	// Use the npm monitor's parse logic for npm.
	if tool == "npm" {
		m := monitors.NewNPMMonitor().(*monitors.NPMMonitor)
		record, err := m.ParseCommand("npm", args)
		if err == nil {
			return record.PackagesAffected
		}
	}

	// Use the go monitor's parse logic for go.
	if tool == "go" {
		m := monitors.NewGoMonitor().(*monitors.GoMonitor)
		record, err := m.ParseCommand("go", args)
		if err == nil {
			return record.PackagesAffected
		}
	}

	// Generic fallback: args[1:] that don't start with '-'.
	var pkgs []string
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			pkgs = append(pkgs, a)
		}
	}
	return pkgs
}

// ── list ───────────────────────────────────────────────────────────────────

func runList(cmd *cobra.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return err
	}
	defer store.Close()

	tool, _ := cmd.Flags().GetString("tool")
	tool = normalizeToolName(tool)
	pkgs, err := store.GetPackages(tool)
	if err != nil {
		return err
	}

	// Filter by unused duration.
	if unusedStr, _ := cmd.Flags().GetString("unused"); unusedStr != "" {
		dur, err := parseDuration(unusedStr)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", unusedStr, err)
		}
		cutoff := time.Now().Add(-dur)
		var filtered []*core.PackageInfo
		for _, p := range pkgs {
			if p.LastUsed.Before(cutoff) {
				filtered = append(filtered, p)
			}
		}
		pkgs = filtered
	}

	if len(pkgs) == 0 {
		fmt.Println(infoStyle.Render("No packages tracked yet. Run `diu scan` to discover installed packages."))
		return nil
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pkgs)
	}

	// Table output grouped by tool.
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Tool != pkgs[j].Tool {
			return pkgs[i].Tool < pkgs[j].Tool
		}
		return pkgs[i].Name < pkgs[j].Name
	})

	fmt.Println(titleStyle.Render("Tracked Packages"))
	fmt.Println()

	currentTool := ""
	for _, pkg := range pkgs {
		if pkg.Tool != currentTool {
			currentTool = pkg.Tool
			toolStyle := lipgloss.NewStyle().Bold(true).Foreground(toolColor(pkg.Tool))
			fmt.Println(toolStyle.Render(pkg.Tool))
		}
		ver := ""
		if pkg.Version != "" {
			ver = " " + subtitleStyle.Render("("+pkg.Version+")")
		}
		lastUsed := pkg.LastUsed.Format("2006-01-02")
		lastUpdated := ""
		if !pkg.LastUpdated.IsZero() {
			lastUpdated = "  updated " + pkg.LastUpdated.Format("2006-01-02")
		}
		fmt.Printf("  %s%s  last used %s%s  uses: %d\n",
			pkg.Name, ver, lastUsed, lastUpdated, pkg.UsageCount)
	}

	return nil
}

// ── stats ──────────────────────────────────────────────────────────────────

func runStats(cmd *cobra.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return err
	}
	defer store.Close()

	stats, err := store.GetStatistics()
	if err != nil {
		return err
	}

	fmt.Println(titleStyle.Render("diu statistics"))
	fmt.Println()
	fmt.Printf("%s %d\n", infoStyle.Render("Total executions recorded:"), stats.TotalExecutions)
	if stats.MostActiveDay != "" {
		fmt.Printf("%s %s\n", infoStyle.Render("Most active day:"), stats.MostActiveDay)
	}

	if len(stats.ExecutionFrequency) > 0 {
		fmt.Println()
		fmt.Println(subtitleStyle.Render("By tool:"))
		for t, count := range stats.ExecutionFrequency {
			ts := lipgloss.NewStyle().Foreground(toolColor(t))
			fmt.Printf("  %s %d\n", ts.Render(t+":"), count)
		}
	}

	top, _ := cmd.Flags().GetInt("top")
	filterTool, _ := cmd.Flags().GetString("tool")
	filterTool = normalizeToolName(filterTool)
	pkgs, _ := store.GetPackages(filterTool)
	if len(pkgs) > 0 && top > 0 {
		sort.Slice(pkgs, func(i, j int) bool {
			return pkgs[i].UsageCount > pkgs[j].UsageCount
		})
		fmt.Println()
		fmt.Printf(subtitleStyle.Render("Top %d packages by usage:\n"), top)
		for i, pkg := range pkgs {
			if i >= top {
				break
			}
			ts := lipgloss.NewStyle().Foreground(toolColor(pkg.Tool))
			fmt.Printf("  %d. %s %s — %d uses, last %s\n",
				i+1, pkg.Name, ts.Render("("+pkg.Tool+")"),
				pkg.UsageCount, pkg.LastUsed.Format("2006-01-02"))
		}
	}

	return nil
}

// ── cleanup ────────────────────────────────────────────────────────────────

func runCleanup(cmd *cobra.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}

	var cutoff time.Time
	if beforeStr, _ := cmd.Flags().GetString("before"); beforeStr != "" {
		dur, err := parseDuration(beforeStr)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", beforeStr, err)
		}
		cutoff = time.Now().Add(-dur)
	} else {
		cutoff = time.Now().AddDate(0, 0, -config.Storage.RetentionDays)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Cleanup(cutoff); err != nil {
		return err
	}
	fmt.Println(successStyle.Render("✓ Cleanup complete"))
	return nil
}

// ── config ─────────────────────────────────────────────────────────────────

func runConfigList(_ *cobra.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(config)
}

func runConfigGet(_ *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("key required")
	}
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "storage.json_file":
		fmt.Println(config.Storage.JSONFile)
	case "storage.retention_days":
		fmt.Println(config.Storage.RetentionDays)
	case "monitoring.enabled_tools":
		fmt.Println(strings.Join(config.Monitoring.EnabledTools, ", "))
	default:
		return fmt.Errorf("unknown key %q", args[0])
	}
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("key and value required")
	}
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "storage.json_file":
		config.Storage.JSONFile = args[1]
	case "storage.retention_days":
		days, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid number: %w", err)
		}
		config.Storage.RetentionDays = days
	case "monitoring.enabled_tools":
		config.Monitoring.EnabledTools = strings.Split(args[1], ",")
	default:
		return fmt.Errorf("unknown key %q", args[0])
	}
	if err := config.Save(""); err != nil {
		return err
	}
	fmt.Println(successStyle.Render("✓ Config updated"))
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────

func toolColor(tool string) lipgloss.Color {
	switch tool {
	case "homebrew":
		return lipgloss.Color("214")
	case "npm":
		return lipgloss.Color("196")
	case "go":
		return lipgloss.Color("86")
	case "pip":
		return lipgloss.Color("226")
	case "gem":
		return lipgloss.Color("160")
	case "cargo":
		return lipgloss.Color("208")
	default:
		return lipgloss.Color("250")
	}
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "m") && !strings.Contains(s, "h") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
