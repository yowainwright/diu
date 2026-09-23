package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/yowainwright/diu/internal/core"
)

func newRecorderSupervisorCommand() *command {
	return &command{
		Use: "record-supervisor", IsHidden: true,
		RunE: func(_ *command, args []string) error {
			return withRecorderSlot(args, superviseRecorder)
		},
	}
}

func newRecorderWorkerCommand() *command {
	return &command{
		Use: "record-worker", IsHidden: true,
		RunE: func(_ *command, args []string) error {
			return withRecorderSlot(args, runRecorderWorker)
		},
	}
}

func withRecorderSlot(args []string, run func(*core.Config, *os.File, int) error) error {
	slot, err := recorderSlotArgument(args)
	if err != nil {
		return err
	}
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	lock, err := inheritedRecorderSlot(config.Daemon.DataDir, slot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return run(config, lock, slot)
}

func recorderSlotArgument(args []string) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("expected inherited recorder slot")
	}
	slot, err := strconv.Atoi(args[0])
	isNumber := err == nil
	isNonnegative := slot >= 0
	isInRange := slot < core.MaxRecorderWorkers
	isValidSlot := isNumber && isNonnegative && isInRange
	if !isValidSlot {
		return 0, fmt.Errorf("invalid recorder slot")
	}
	return slot, nil
}
