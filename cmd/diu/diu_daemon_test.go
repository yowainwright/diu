package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestWaitForDaemonProcessStopped(t *testing.T) {
	deadPID := 999999999
	if err := waitForDaemonProcessStopped(deadPID, time.Second); err != nil {
		t.Fatalf("dead process wait failed: %v", err)
	}
	err := waitForDaemonProcessStopped(os.Getpid(), 0)
	hasWaitError := err != nil && strings.Contains(err.Error(), "waiting for daemon process")
	if !hasWaitError {
		t.Fatalf("live process wait error = %v", err)
	}
}
