//go:build stress

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestFallbackBurstNearStorageLimit(t *testing.T) {
	binDir := buildFallbackTestBinary(t)
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			seedNearLimitHistory(t, config)
			pidLog := installRecorderProbe(t, binDir)
			wrapper := installFallbackTestWrapper(t, config, writeFallbackOriginal(t), template)
			runFallbackBurst(t, wrapper)
			assertRecordersExited(t, pidLog)
			waitForBurstRecorderSlots(t, config)
			assertBurstHistory(t, config)
		})
	}
}

func seedNearLimitHistory(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	const count = 16063
	records := make([]*core.ExecutionRecord, count)
	for index := range records {
		records[index] = &core.ExecutionRecord{ID: fmt.Sprintf("seed-%05d", index), Tool: "seed", Command: strings.Repeat("x", 480), Timestamp: time.Now()}
	}
	batchStore := store.(*storage.JSONStorage)
	if err := batchStore.AddExecutions(records); err != nil {
		t.Fatal(err)
	}
	assertNearLimitHistory(t, config, count)
}

func assertNearLimitHistory(t *testing.T, config *core.Config, count int) {
	t.Helper()
	info, err := os.Stat(storage.ExecutionLogPath(config.Storage.JSONFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d records, %d bytes", count, info.Size())
	limit := config.Storage.MaxStorageBytes
	outsideTarget := info.Size() < limit*95/100 || info.Size() > limit
	if outsideTarget {
		t.Fatalf("fixture size %d is not near storage limit %d", info.Size(), limit)
	}
}

func installRecorderProbe(t *testing.T, binDir string) string {
	t.Helper()
	probeDir := t.TempDir()
	pidLog := filepath.Join(probeDir, "recorders")
	t.Setenv("DIU_TEST_RECORDERS", pidLog)
	t.Setenv("DIU_TEST_RECORDER", filepath.Join(binDir, "diu"))
	script := "#!/bin/sh\nprintf '%s\\n' \"$$\" >> \"$DIU_TEST_RECORDERS\"\nexec \"$DIU_TEST_RECORDER\" \"$@\"\n"
	writeExecutableForTest(t, filepath.Join(probeDir, "diu"), script)
	t.Setenv("PATH", probeDir+":/usr/bin:/bin")
	return pidLog
}

func runFallbackBurst(t *testing.T, wrapper string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	results := make(chan error, 32)
	for range cap(results) {
		go func() { results <- runBurstWrapper(ctx, wrapper) }()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	t.Logf("32 wrappers completed in %s", time.Since(started))
}

func runBurstWrapper(ctx context.Context, wrapper string) error {
	cmd := exec.CommandContext(ctx, wrapper)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	correctExit := errors.As(err, &exitErr) && exitErr.ExitCode() == 7
	correctOutput := string(output) == "original output\noriginal error\n"
	changedBehavior := !correctExit || !correctOutput
	if changedBehavior {
		return fmt.Errorf("wrapper changed output or exit: %q, %v", output, err)
	}
	return nil
}

func assertRecordersExited(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pids := strings.Fields(string(data))
	if len(pids) != 32 {
		t.Fatalf("started %d recorders, want 32", len(pids))
	}
	for _, value := range pids {
		pid, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("recorder %d still exists: %v", pid, err)
		}
	}
}

func waitForBurstRecorderSlots(t *testing.T, config *core.Config) {
	t.Helper()
	deadline := time.Now().Add(core.RecorderTimeout + time.Second)
	for time.Now().Before(deadline) {
		if burstRecorderSlotsAreFree(t, config.Daemon.DataDir) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background recorders did not release their slots")
}

func burstRecorderSlotsAreFree(t *testing.T, dataDir string) bool {
	t.Helper()
	for slot := range core.MaxRecorderWorkers {
		lock, err := tryRecorderSlot(core.RecorderSlotPath(dataDir, slot))
		if err != nil {
			t.Fatal(err)
		}
		if lock == nil {
			return false
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return true
}

func assertBurstHistory(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Tool != "seed" {
			return
		}
	}
	t.Fatal("burst recorded no executions")
}
