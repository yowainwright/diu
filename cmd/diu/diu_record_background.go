package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/yowainwright/diu/internal/core"
)

func startBackgroundRecorder(config *core.Config, input io.Reader) error {
	lock, slot, err := acquireRecorderSlot(config.Daemon.DataDir)
	failedToAcquireSlot := err != nil || lock == nil
	if failedToAcquireSlot {
		return err
	}
	defer func() { _ = lock.Close() }()
	payload, err := recorderPayloadFile(config.Daemon.DataDir, input)
	if err != nil {
		return err
	}
	defer func() { _ = payload.Close() }()
	return detachRecorderSupervisor(lock, slot, payload)
}

func recorderPayloadFile(dataDir string, input io.Reader) (*os.File, error) {
	file, err := os.CreateTemp(dataDir, ".recorder-payload-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := copyRecorderPayload(file, input); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func copyRecorderPayload(file *os.File, input io.Reader) error {
	size, err := io.Copy(file, io.LimitReader(input, core.MaxRecorderPayloadBytes+1))
	if err != nil {
		return err
	}
	if size > core.MaxRecorderPayloadBytes {
		return fmt.Errorf("recorder payload exceeds size limit")
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func detachRecorderSupervisor(lock *os.File, slot int, payload *os.File) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// #nosec G204 -- re-exec this DIU binary with a fixed internal command and bounded slot.
	cmd := exec.Command(executable, "record-supervisor", strconv.Itoa(slot))
	configureRecorderProcess(cmd, lock, payload)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func configureRecorderProcess(cmd *exec.Cmd, lock *os.File, input *os.File) {
	cmd.Stdin = input
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Env = append(os.Environ(), "DIU_RECORDING=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
