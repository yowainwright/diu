package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	rootCmd := &command{
		Use:   "diu",
		Short: "Do I Use - Package Manager Execution Tracker",
		Long:  `DIU tracks when package managers and global development tools are executed, storing execution data for analysis and auditing.`,
	}

	// Daemon commands
	daemonCmd := &command{
		Use:   "daemon",
		Short: "Manage the DIU daemon",
	}

	daemonStartCmd := &command{
		Use:   "start",
		Short: "Start the DIU daemon",
		RunE:  startDaemon,
	}

	daemonStopCmd := &command{
		Use:   "stop",
		Short: "Stop the DIU daemon",
		RunE:  stopDaemon,
	}

	daemonRestartCmd := &command{
		Use:   "restart",
		Short: "Restart the DIU daemon",
		RunE:  restartDaemon,
	}

	daemonStatusCmd := &command{
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

	queryCmd := &command{
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

	statsCmd := &command{
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

	packagesCmd := &command{
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

	checkCmd := &command{
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

	manageCmd := &command{
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
	configCmd := &command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configGetCmd := &command{
		Use:   "get [key]",
		Short: "Get configuration value",
		RunE:  getConfig,
	}

	configSetCmd := &command{
		Use:   "set [key] [value]",
		Short: "Set configuration value",
		RunE:  setConfig,
	}

	configListCmd := &command{
		Use:   "list",
		Short: "List all configuration",
		RunE:  listConfig,
	}

	configCmd.AddCommand(configGetCmd, configSetCmd, configListCmd)

	// Maintenance commands
	cleanupCmd := &command{
		Use:   "cleanup",
		Short: "Clean executions based on retention and storage limits",
		RunE:  cleanup,
	}

	backupCmd := &command{
		Use:   "backup",
		Short: "Create manual backup",
		RunE:  backup,
	}

	setupCmd := &command{
		Use:   "setup",
		Short: "Install wrappers and initialize local storage",
		RunE:  setupProject,
	}

	scanCmd := &command{
		Use:   "scan",
		Short: "Scan installed packages into inventory",
		RunE:  scanPackages,
	}

	recordCmd := &command{
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

	if err := rootCmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.RenderTo(err.Error(), os.Stderr))
		os.Exit(1)
	}
}
