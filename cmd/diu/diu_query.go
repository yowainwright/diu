package main

import (
	"cmp"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
)

type executionSummarizer interface {
	SummarizeExecutions(storage.QueryOptions) (storage.ExecutionSummary, error)
}

type statsCommandOptions struct {
	query            storage.QueryOptions
	shouldShowDaily  bool
	shouldShowWeekly bool
	tool             string
	top              int
}

func queryExecutions(cmd *command, args []string) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer closeStore(store)

	opts, err := executionQueryOptions(cmd)
	if err != nil {
		return err
	}

	executions, err := store.GetExecutions(opts)
	if err != nil {
		return fmt.Errorf("failed to query executions: %w", err)
	}

	return printExecutions(cmd, executions)
}

func executionQueryOptions(cmd *command) (storage.QueryOptions, error) {
	opts := storage.QueryOptions{
		Tool:    core.NormalizeToolName(cmd.Flag("tool").Value.String()),
		Package: cmd.Flag("package").Value.String(),
	}

	limit, _ := cmd.Flags().GetInt("limit")
	opts.Limit = limit

	if lastStr, _ := cmd.Flags().GetString("last"); lastStr != "" {
		duration, err := parseDuration(lastStr)
		if err != nil {
			return opts, fmt.Errorf("invalid duration: %w", err)
		}
		since := time.Now().Add(-duration)
		opts.Since = &since
	}
	return opts, nil
}

func printExecutions(cmd *command, executions []*core.ExecutionRecord) error {
	format, _ := cmd.Flags().GetString("format")
	switch format {
	case "json":
		return printExecutionsJSON(executions)
	case "csv":
		return printExecutionsCSV(executions)
	default:
		printExecutionsTable(executions)
		return nil
	}
}

func printExecutionsJSON(executions []*core.ExecutionRecord) error {
	enc := json.NewEncoder(cliOutput().Stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(executions)
}

func printExecutionsCSV(executions []*core.ExecutionRecord) error {
	writer := csv.NewWriter(cliOutput().Stdout())
	header := []string{"tool", "command", "timestamp", "duration_ms", "exit_code", "working_dir"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, exec := range executions {
		if err := writer.Write(executionCSVRow(exec)); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func executionCSVRow(exec *core.ExecutionRecord) []string {
	return []string{
		exec.Tool,
		exec.Command,
		exec.Timestamp.Format(time.RFC3339),
		fmt.Sprintf("%d", exec.Duration.Milliseconds()),
		fmt.Sprintf("%d", exec.ExitCode),
		exec.WorkingDir,
	}
}

func printExecutionsTable(executions []*core.ExecutionRecord) {
	out := cliOutput()
	if len(executions) == 0 {
		out.Println(out.StyleData(dx.Info, "No executions found"))
		return
	}

	out.Println(out.StyleData(dx.Accent, "Execution History"))
	out.Println()
	for _, exec := range executions {
		printExecutionRow(out, exec)
	}
}

func printExecutionRow(out *dx.Out, exec *core.ExecutionRecord) {
	out.Printf("%s %s %s\n",
		exec.Timestamp.Format("2006-01-02 15:04:05"),
		out.StyleData(dx.Accent, fmt.Sprintf("[%s]", exec.Tool)),
		exec.Command,
	)
	printExecutionPackages(out, exec)
	printExecutionLocation(out, exec)
	printExecutionExitCode(out, exec)
	out.Println()
}

func printExecutionPackages(out *dx.Out, exec *core.ExecutionRecord) {
	if len(exec.PackagesAffected) == 0 {
		return
	}
	out.Printf("  %s %s\n",
		out.StyleData(dx.Muted, "Packages:"),
		strings.Join(exec.PackagesAffected, ", "),
	)
}

func printExecutionLocation(out *dx.Out, exec *core.ExecutionRecord) {
	if exec.WorkingDir == "" {
		return
	}
	out.Printf("  %s %s\n",
		out.StyleData(dx.Muted, "Location:"),
		displayLocalPath(exec.WorkingDir),
	)
}

func printExecutionExitCode(out *dx.Out, exec *core.ExecutionRecord) {
	if exec.ExitCode == 0 {
		return
	}
	out.Printf("  %s %d\n",
		out.StyleData(dx.Error, "Exit code:"),
		exec.ExitCode,
	)
}

func showStats(cmd *command, args []string) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer closeStore(store)

	options := statsOptionsFromCommand(cmd)
	return printStats(store, options)
}

func printStats(store storage.Storage, options statsCommandOptions) error {
	out := cliOutput()
	printStatsHeading(out, options)
	summary, err := summarizeExecutions(store, options.query)
	if err != nil {
		return fmt.Errorf("failed to summarize executions: %w", err)
	}
	printStatsSummary(out, summary)

	stats, _ := store.Statistics()
	printMostActiveDay(out, stats, options)
	printToolCounts(out, summary.ToolCounts)
	printTopPackages(store, out, options)
	return nil
}

func statsOptionsFromCommand(cmd *command) statsCommandOptions {
	shouldShowDaily, _ := cmd.Flags().GetBool("daily")
	shouldShowWeekly, _ := cmd.Flags().GetBool("weekly")
	toolFilter, _ := cmd.Flags().GetString("tool")
	top, _ := cmd.Flags().GetInt("top")

	opts := storage.QueryOptions{}
	if toolFilter != "" {
		opts.Tool = core.NormalizeToolName(toolFilter)
	}
	if shouldShowDaily {
		since := time.Now().Add(-24 * time.Hour)
		opts.Since = &since
	} else if shouldShowWeekly {
		since := time.Now().Add(-7 * 24 * time.Hour)
		opts.Since = &since
	}
	return statsCommandOptions{query: opts, shouldShowDaily: shouldShowDaily, shouldShowWeekly: shouldShowWeekly, tool: toolFilter, top: top}
}

func printStatsHeading(out *dx.Out, options statsCommandOptions) {
	title := "DIU Statistics"
	if options.shouldShowDaily {
		title = "DIU Statistics (Last 24 Hours)"
	} else if options.shouldShowWeekly {
		title = "DIU Statistics (Last 7 Days)"
	}
	out.Println(out.StyleData(dx.Accent, title))
	out.Println()
}

func printStatsSummary(out *dx.Out, summary storage.ExecutionSummary) {
	out.Printf("%s %d\n",
		out.StyleData(dx.Info, "Total executions:"),
		summary.Total,
	)
}

func printMostActiveDay(out *dx.Out, stats *core.StorageStatistics, options statsCommandOptions) {
	if !shouldPrintMostActiveDay(stats, options) {
		return
	}
	out.Printf("%s %s\n",
		out.StyleData(dx.Info, "Most active day:"),
		stats.MostActiveDay,
	)
}

func shouldPrintMostActiveDay(stats *core.StorageStatistics, options statsCommandOptions) bool {
	if stats.MostActiveDay == "" {
		return false
	}
	if options.shouldShowDaily {
		return false
	}
	return !options.shouldShowWeekly
}

func printToolCounts(out *dx.Out, toolCounts map[string]int) {
	out.Println()
	out.Println(out.StyleData(dx.Muted, "Tool usage:"))
	tools := sortedToolCountKeys(toolCounts)
	for _, tool := range tools {
		count := toolCounts[tool]
		out.Printf("  %s %d\n", out.StyleData(dx.Accent, tool+":"), count)
	}
}

func sortedToolCountKeys(toolCounts map[string]int) []string {
	tools := make([]string, 0, len(toolCounts))
	for tool := range toolCounts {
		tools = append(tools, tool)
	}
	slices.Sort(tools)
	return tools
}

func printTopPackages(store storage.Storage, out *dx.Out, options statsCommandOptions) {
	if options.top <= 0 {
		return
	}
	packages, _ := store.GetPackages(core.NormalizeToolName(options.tool))
	slices.SortFunc(packages, comparePackageUsage)
	out.Println()
	out.Printf(out.StyleData(dx.Muted, "Top %d packages:\n"), options.top)
	for i, pkg := range packages {
		if i >= options.top {
			break
		}
		printTopPackage(out, i, pkg)
	}
}

func comparePackageUsage(a, b *core.PackageInfo) int {
	if order := cmp.Compare(b.UsageCount, a.UsageCount); order != 0 {
		return order
	}
	return strings.Compare(a.Name, b.Name)
}

func printTopPackage(out *dx.Out, index int, pkg *core.PackageInfo) {
	out.Printf("  %d. %s (%s) - used %d times\n",
		index+1,
		pkg.Name,
		pkg.Tool,
		pkg.UsageCount,
	)
}

func summarizeExecutions(store storage.Storage, opts storage.QueryOptions) (storage.ExecutionSummary, error) {
	if summarizer, ok := store.(executionSummarizer); ok {
		return summarizer.SummarizeExecutions(opts)
	}
	executions, err := store.GetExecutions(opts)
	if err != nil {
		return storage.ExecutionSummary{}, err
	}
	toolCounts := make(map[string]int)
	for _, execution := range executions {
		toolCounts[execution.Tool]++
	}
	summary := storage.ExecutionSummary{Total: len(executions), ToolCounts: toolCounts}
	return summary, nil
}
