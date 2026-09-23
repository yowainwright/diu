package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func superviseRecorder(_ *core.Config, lock *os.File, slot int) error {
	ctx, cancel := context.WithTimeout(context.Background(), core.RecorderTimeout)
	defer cancel()
	cmd, err := supervisedRecorderCommand(ctx, lock, slot)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = killRecorderGroup(cmd.Process.Pid) }()
	return cmd.Wait()
}

func supervisedRecorderCommand(ctx context.Context, lock *os.File, slot int) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, "record-worker", strconv.Itoa(slot))
	configureRecorderProcess(cmd, lock, os.Stdin)
	cmd.Cancel = func() error { return killRecorderGroup(cmd.Process.Pid) }
	cmd.WaitDelay = time.Second
	return cmd, nil
}

func killRecorderGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != syscall.ESRCH {
		return err
	}
	return nil
}
