package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
)

type executionSummarizer interface {
	SummarizeExecutions(storage.QueryOptions) (storage.ExecutionSummary, error)
}

// queryExecutions queries and displays execution history
func queryExecutions(cmd *command, args []string) error {
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
	out := cliOutput()
	switch format {
	case "json":
		enc := json.NewEncoder(out.Stdout())
		enc.SetIndent("", "  ")
		return enc.Encode(executions)

	case "csv":
		writer := csv.NewWriter(out.Stdout())
		if err := writer.Write([]string{"tool", "command", "timestamp", "duration_ms", "exit_code", "working_dir"}); err != nil {
			return err
		}
		for _, exec := range executions {
			if err := writer.Write([]string{
				exec.Tool,
				exec.Command,
				exec.Timestamp.Format(time.RFC3339),
				fmt.Sprintf("%d", exec.Duration.Milliseconds()),
				fmt.Sprintf("%d", exec.ExitCode),
				exec.WorkingDir,
			}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()

	default: // table
		if len(executions) == 0 {
			out.Println(out.DataStyle(dx.Info, "No executions found"))
			return nil
		}

		out.Println(out.DataStyle(dx.Accent, "Execution History"))
		out.Println()

		for _, exec := range executions {
			out.Printf("%s %s %s\n",
				exec.Timestamp.Format("2006-01-02 15:04:05"),
				out.DataStyle(dx.Accent, fmt.Sprintf("[%s]", exec.Tool)),
				exec.Command,
			)

			if len(exec.PackagesAffected) > 0 {
				out.Printf("  %s %s\n",
					out.DataStyle(dx.Muted, "Packages:"),
					strings.Join(exec.PackagesAffected, ", "),
				)
			}

			if exec.WorkingDir != "" {
				out.Printf("  %s %s\n",
					out.DataStyle(dx.Muted, "Location:"),
					displayLocalPath(exec.WorkingDir),
				)
			}

			if exec.ExitCode != 0 {
				out.Printf("  %s %d\n",
					out.DataStyle(dx.Error, "Exit code:"),
					exec.ExitCode,
				)
			}
			out.Println()
		}
	}

	return nil
}

// showStats displays usage statistics
func showStats(cmd *command, args []string) error {
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
	out := cliOutput()
	if toolFilter != "" {
		opts.Tool = core.NormalizeToolName(toolFilter)
	}

	if daily {
		since := time.Now().Add(-24 * time.Hour)
		opts.Since = &since
		out.Println(out.DataStyle(dx.Accent, "DIU Statistics (Last 24 Hours)"))
	} else if weekly {
		since := time.Now().Add(-7 * 24 * time.Hour)
		opts.Since = &since
		out.Println(out.DataStyle(dx.Accent, "DIU Statistics (Last 7 Days)"))
	} else {
		out.Println(out.DataStyle(dx.Accent, "DIU Statistics"))
	}
	out.Println()

	summary, err := summarizeExecutions(store, opts)
	if err != nil {
		return fmt.Errorf("failed to summarize executions: %w", err)
	}

	out.Printf("%s %d\n",
		out.DataStyle(dx.Info, "Total executions:"),
		summary.Total,
	)

	stats, _ := store.GetStatistics()
	if stats.MostActiveDay != "" && !daily && !weekly {
		out.Printf("%s %s\n",
			out.DataStyle(dx.Info, "Most active day:"),
			stats.MostActiveDay,
		)
	}

	out.Println()
	out.Println(out.DataStyle(dx.Muted, "Tool usage:"))
	for tool, count := range summary.ToolCounts {
		out.Printf("  %s %d\n", out.DataStyle(dx.Accent, tool+":"), count)
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
		out.Println()
		out.Printf(out.DataStyle(dx.Muted, "Top %d packages:\n"), top)

		for i, pkg := range packages {
			if i >= top {
				break
			}
			out.Printf("  %d. %s (%s) - used %d times\n",
				i+1,
				pkg.Name,
				pkg.Tool,
				pkg.UsageCount,
			)
		}
	}

	return nil
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
