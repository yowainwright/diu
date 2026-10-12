package record

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
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

const (
	defaultBackgroundEnabled   = false
	fallbackRecordLockSuffix   = ".fallback.lock"
	fallbackRecordLockAttempts = 5
	fallbackRecordRetryDelay   = 10 * time.Millisecond
	fallbackRecordLockWait     = fallbackRecordLockAttempts * fallbackRecordRetryDelay
)

func NewCommand() *dx.Command {
	var shouldUseBackground bool
	cmd := &dx.Command{
		Use:      "record",
		Short:    "Record an execution event from stdin",
		IsHidden: true,
		RunE:     RunExecution,
	}
	cmd.Flags().BoolVar(&shouldUseBackground, "background", defaultBackgroundEnabled, "Admit bounded background recording")
	return cmd
}

func RunExecution(cmd *dx.Command, _ []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	background, _ := cmd.Flags().GetBool("background")
	if background {
		return startBackgroundRecorder(config, dx.TerminalOut().Stdin())
	}
	return recordFallbackExecution(config, dx.TerminalOut().Stdin())
}

func storeFallbackExecutionFrom(config *core.Config, wait time.Duration, input io.Reader) error {
	var record core.ExecutionRecord
	if err := json.NewDecoder(input).Decode(&record); err != nil {
		return fmt.Errorf("failed to decode execution record: %w", err)
	}

	enrichRecord(config, &record)

	store, err := storage.NewJSONStorageWithLockWait(config, wait)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStorage(store)

	if err := store.AddExecution(&record); err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

func recordFallbackExecution(config *core.Config, input io.Reader) (err error) {
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
	return storeFallbackExecutionFrom(config, wait, input)
}

func acquireFallbackRecordLock(storagePath string) (*os.File, time.Duration, error) {
	wait := fallbackRecordLockWait
	for attempt := 0; attempt < fallbackRecordLockAttempts; attempt++ {
		lock, err := tryAcquireFallbackRecordLock(storagePath)
		finished := err != nil || lock != nil
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

func tryAcquireFallbackRecordLock(storagePath string) (*os.File, error) {
	lock, err := openFallbackRecordLock(storagePath)
	if err != nil {
		return nil, err
	}
	if err := lockFallbackRecorder(lock); err != nil {
		_ = lock.Close()
		if isFallbackLockContention(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to lock fallback recorder: %w", err)
	}
	return lock, nil
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

func NewSupervisorCommand() *dx.Command {
	return &dx.Command{
		Use: "record-supervisor", IsHidden: true,
		RunE: func(_ *dx.Command, args []string) error {
			return withRecorderSlot(args, superviseRecorder)
		},
	}
}

func NewWorkerCommand() *dx.Command {
	return &dx.Command{
		Use: "record-worker", IsHidden: true,
		RunE: func(_ *dx.Command, args []string) error {
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

func closeStorage(store storage.Storage) {
	if err := store.Close(); err != nil {
		dx.TerminalOut().Status(dx.Error, fmt.Sprintf("failed to close storage: %v", err))
	}
}

func enrichRecord(config *core.Config, record *core.ExecutionRecord) {
	record.Tool = core.NormalizeToolName(record.Tool)
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Kind == core.ExecutionKindLaunchAttempt {
		return
	}
	monitor, err := monitors.NewMonitor(record.Tool)
	if err != nil {
		return
	}
	if err := monitor.Initialize(config); err != nil {
		return
	}
	monitors.EnrichExecutionRecord(monitor, record)
}
