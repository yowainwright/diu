package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

const (
	fallbackRecordLockSuffix   = ".fallback.lock"
	fallbackRecordLockAttempts = 5
	fallbackRecordRetryDelay   = 10 * time.Millisecond
	fallbackRecordLockWait     = fallbackRecordLockAttempts * fallbackRecordRetryDelay
)

func recordExecution(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if flagBool(cmd, "background") {
		return startBackgroundRecorder(config, cliOutput().Stdin())
	}
	return withFallbackRecordLock(config, func(wait time.Duration) error {
		return storeFallbackExecution(config, wait)
	})
}

func storeFallbackExecution(config *core.Config, wait time.Duration) error {
	return storeFallbackExecutionFrom(config, wait, cliOutput().Stdin())
}

func storeFallbackExecutionFrom(config *core.Config, wait time.Duration, input io.Reader) error {
	var record core.ExecutionRecord
	if err := json.NewDecoder(input).Decode(&record); err != nil {
		return fmt.Errorf("failed to decode execution record: %w", err)
	}

	enrichExecutionRecord(config, &record)

	store, err := storage.NewJSONStorageWithLockWait(config, wait)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.AddExecution(&record); err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

func withFallbackRecordLock(config *core.Config, record func(time.Duration) error) (err error) {
	lock, wait, err := acquireFallbackRecordLock(config.Storage.JSONFile)
	if err != nil {
		return err
	}
	if lock == nil {
		_ = observability.MarkFallbackContention(config.Daemon.DataDir)
		return fmt.Errorf("fallback recorder remained busy after %d attempts", fallbackRecordLockAttempts)
	}
	defer func() {
		if errors.Is(err, context.DeadlineExceeded) {
			_ = observability.MarkFallbackContention(config.Daemon.DataDir)
		}
		err = errors.Join(err, releaseFallbackRecordLock(lock))
	}()
	return record(wait)
}

func acquireFallbackRecordLock(storagePath string) (*os.File, time.Duration, error) {
	wait := fallbackRecordLockWait
	for attempt := 0; attempt < fallbackRecordLockAttempts; attempt++ {
		lock, acquired, err := tryAcquireFallbackRecordLock(storagePath)
		finished := err != nil || acquired
		if finished {
			return lock, wait, err
		}
		canRetry := attempt+1 < fallbackRecordLockAttempts && wait > 0
		if !canRetry {
			return nil, 0, nil
		}
		wait = waitForFallbackRecordRetry(wait)
	}
	return nil, 0, nil
}

func waitForFallbackRecordRetry(wait time.Duration) time.Duration {
	started := time.Now()
	time.Sleep(min(fallbackRecordRetryDelay, wait))
	return max(0, wait-time.Since(started))
}

func tryAcquireFallbackRecordLock(storagePath string) (*os.File, bool, error) {
	lock, err := openFallbackRecordLock(storagePath)
	if err != nil {
		return nil, false, err
	}
	if err := lockFallbackRecorder(lock); err != nil {
		_ = lock.Close()
		if isFallbackLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to lock fallback recorder: %w", err)
	}
	return lock, true, nil
}

func openFallbackRecordLock(storagePath string) (*os.File, error) {
	lockPath := storagePath + fallbackRecordLockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), core.OwnerDirectoryMode); err != nil {
		return nil, fmt.Errorf("failed to create fallback lock directory: %w", err)
	}
	lock, err := safefs.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open fallback lock: %w", err)
	}
	if err := lock.Chmod(core.PrivateFileMode); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("failed to secure fallback lock: %w", err)
	}
	return lock, nil
}

func lockFallbackRecorder(lock *os.File) error {
	return syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func isFallbackLockContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func releaseFallbackRecordLock(lock *os.File) error {
	unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	closeErr := lock.Close()
	return errors.Join(unlockErr, closeErr)
}

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
