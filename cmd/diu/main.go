package main

import (
	"os"

	"github.com/yowainwright/diu/internal/dx"
)

const defaultBoolFlagValue = false

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newUninstallCommand() *command {
	return &command{
		Use:   "uninstall",
		Short: "Remove wrappers and shell PATH entries",
		RunE:  uninstallProject,
	}
}

func main() {
	err := newRootCommand().Execute(os.Args[1:])
	if err == nil {
		return
	}
	cliOutput().Status(dx.Error, err.Error())
	os.Exit(exitStatus(err))
}

func newRootCommand() *command {
	rootCmd := rootCommand()
	rootCmd.AddCommand(rootCommands()...)
	return rootCmd
}

func rootCommand() *command {
	var styleguide bool
	rootCmd := &command{
		Use:     "diu",
		Short:   "Do I Use - Package Manager Execution Tracker",
		Long:    `DIU tracks when package managers and global development tools are executed, storing execution data for analysis and auditing.`,
		RunE:    runRootCommand,
		Version: versionString,
	}
	rootCmd.Flags().BoolVar(&styleguide, "styleguide", defaultBoolFlagValue, "Show CLI style guide")
	return rootCmd
}

func rootCommands() []*command {
	return []*command{
		newDaemonCommand(),
		newQueryCommand(),
		newStatsCommand(),
		newPackagesCommand(),
		newCheckCommand(),
		newManageCommand(),
		newConfigCommand(),
		newCleanupCommand(),
		newBackupCommand(),
		newStatusCommand(),
		newDiagnosticsCommand(),
		newSetupCommand(),
		newUninstallCommand(),
		newScanCommand(),
		newRecordCommand(),
	}
}

func newDaemonCommand() *command {
	daemonCmd := &command{
		Use:   "daemon",
		Short: "Manage the DIU daemon",
	}
	daemonCmd.AddCommand(
		newDaemonStartCommand(),
		newDaemonStopCommand(),
		newDaemonRestartCommand(),
		newDaemonStatusCommand(),
	)
	return daemonCmd
}

func newDaemonStartCommand() *command {
	return &command{
		Use:   "start",
		Short: "Start the DIU daemon",
		RunE:  startDaemon,
	}
}

func newDaemonStopCommand() *command {
	return &command{
		Use:   "stop",
		Short: "Stop the DIU daemon",
		RunE:  stopDaemon,
	}
}

func newDaemonRestartCommand() *command {
	return &command{
		Use:   "restart",
		Short: "Restart the DIU daemon",
		RunE:  restartDaemon,
	}
}

func newDaemonStatusCommand() *command {
	return &command{
		Use:   "status",
		Short: "Check daemon status",
		RunE:  daemonStatus,
	}
}

func newQueryCommand() *command {
	queryCmd := &command{
		Use:   "query",
		Short: "Query execution history",
		RunE:  queryExecutions,
	}
	addQueryFlags(queryCmd)
	return queryCmd
}

func addQueryFlags(queryCmd *command) {
	var (
		queryTool    string
		queryPackage string
		queryLast    string
		queryLimit   int
		queryFormat  string
	)
	queryCmd.Flags().StringVarP(&queryTool, "tool", "t", "", "Filter by tool (brew, npm, go, etc.)")
	queryCmd.Flags().StringVarP(&queryPackage, "package", "p", "", "Filter by package name")
	queryCmd.Flags().StringVarP(&queryLast, "last", "l", "", "Show executions in last duration (e.g., 24h, 7d)")
	queryCmd.Flags().IntVarP(&queryLimit, "limit", "n", 20, "Limit number of results")
	queryCmd.Flags().StringVarP(&queryFormat, "format", "f", "table", "Output format (table, json, csv)")
}

func newStatsCommand() *command {
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
	statsCmd.Flags().BoolVarP(&statsDaily, "daily", "d", defaultBoolFlagValue, "Show daily statistics")
	statsCmd.Flags().BoolVarP(&statsWeekly, "weekly", "w", defaultBoolFlagValue, "Show weekly statistics")
	statsCmd.Flags().StringVarP(&statsTool, "tool", "t", "", "Statistics for specific tool")
	statsCmd.Flags().IntVar(&statsTop, "top", 10, "Show top N most used packages")
	return statsCmd
}

func newPackagesCommand() *command {
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
	return packagesCmd
}

func newCheckCommand() *command {
	checkCmd := &command{
		Use:   "check [search]",
		Short: "Check installed package usage",
		RunE:  checkPackages,
	}
	addCheckFlags(checkCmd)
	return checkCmd
}

func addCheckFlags(checkCmd *command) {
	var (
		checkTool   string
		checkSearch string
		checkUnused string
		checkLimit  int
		checkFormat string
	)
	checkCmd.Flags().StringVarP(&checkTool, "tool", "t", "", "Filter by tool")
	checkCmd.Flags().StringVarP(&checkSearch, "search", "s", "", "Search package names")
	checkCmd.Flags().StringVarP(&checkUnused, "unused", "u", "", "Show packages not used in duration")
	checkCmd.Flags().IntVarP(&checkLimit, "limit", "n", defaultListLimit, "Limit non-interactive results")
	checkCmd.Flags().StringVarP(&checkFormat, "format", "f", formatTable, "Output format (table, json, csv)")
}

func newManageCommand() *command {
	manageCmd := &command{
		Use:   "manage [search]",
		Short: "Search and uninstall installed packages",
		RunE:  managePackages,
	}
	addManageFlags(manageCmd)
	return manageCmd
}

func addManageFlags(manageCmd *command) {
	var (
		manageTool      string
		manageSearch    string
		manageUninstall string
		manageYes       bool
		manageDryRun    bool
	)
	manageCmd.Flags().StringVarP(&manageTool, "tool", "t", "", "Filter by tool")
	manageCmd.Flags().StringVarP(&manageSearch, "search", "s", "", "Search package names")
	manageCmd.Flags().StringVar(&manageUninstall, "uninstall", "", "Uninstall package non-interactively")
	manageCmd.Flags().BoolVarP(&manageYes, "yes", "y", defaultBoolFlagValue, "Skip uninstall confirmation")
	manageCmd.Flags().BoolVar(&manageDryRun, "dry-run", defaultBoolFlagValue, "Print uninstall command without running it")
}

func newConfigCommand() *command {
	configCmd := &command{
		Use:   "config",
		Short: "Manage configuration",
	}
	configCmd.AddCommand(newConfigGetCommand(), newConfigSetCommand(), newConfigListCommand())
	return configCmd
}

func newConfigGetCommand() *command {
	return &command{
		Use:   "get [key]",
		Short: "Get configuration value",
		RunE:  getConfig,
	}
}

func newConfigSetCommand() *command {
	return &command{
		Use:   "set [key] [value]",
		Short: "Set configuration value",
		RunE:  setConfig,
	}
}

func newConfigListCommand() *command {
	return &command{
		Use:   "list",
		Short: "List all configuration",
		RunE:  listConfig,
	}
}

func newCleanupCommand() *command {
	return &command{
		Use:   "cleanup",
		Short: "Clean executions based on retention and storage limits",
		RunE:  cleanup,
	}
}

func newBackupCommand() *command {
	return &command{
		Use:   "backup",
		Short: "Create manual backup",
		RunE:  backup,
	}
}

func newDiagnosticsCommand() *command {
	var diagnosticsOutput string
	diagnosticsCmd := &command{
		Use:   "diagnostics",
		Short: "Create a redacted local diagnostic report",
		RunE:  diagnostics,
	}
	diagnosticsCmd.Flags().StringVarP(&diagnosticsOutput, "output", "o", "", "Write report to a new private file")
	return diagnosticsCmd
}

func newStatusCommand() *command {
	return &command{
		Use:   "status",
		Short: "Show DIU usage, activity, and local paths",
		RunE:  showStatus,
	}
}

func newSetupCommand() *command {
	return &command{
		Use:   "setup",
		Short: "Install wrappers and initialize local storage",
		RunE:  setupProject,
	}
}

func newScanCommand() *command {
	return &command{
		Use:   "scan",
		Short: "Scan installed packages into inventory",
		RunE:  scanPackages,
	}
}

func newRecordCommand() *command {
	return &command{
		Use:    "record",
		Short:  "Record an execution event from stdin",
		Hidden: true,
		RunE:   recordExecution,
	}
}

func exitStatus(err error) int {
	code, ok := dx.ExitCode(err)
	if !ok {
		return 1
	}
	if code < 1 {
		return 1
	}
	return code
}
