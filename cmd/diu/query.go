package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

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
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(executions)

	case "csv":
		writer := csv.NewWriter(os.Stdout)
		if err := writer.Write([]string{"tool", "command", "timestamp", "duration_ms", "exit_code"}); err != nil {
			return err
		}
		for _, exec := range executions {
			if err := writer.Write([]string{
				exec.Tool,
				exec.Command,
				exec.Timestamp.Format(time.RFC3339),
				fmt.Sprintf("%d", exec.Duration.Milliseconds()),
				fmt.Sprintf("%d", exec.ExitCode),
			}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()

	default: // table
		if len(executions) == 0 {
			fmt.Println(infoStyle.Render("No executions found"))
			return nil
		}

		fmt.Println(titleStyle.Render("Execution History"))
		fmt.Println()

		for _, exec := range executions {
			toolColor := getToolColor(exec.Tool)
			toolStyle := newStyle().Foreground(toolColor)

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
		toolStyle := newStyle().Foreground(toolColor)
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
