package record

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestRecorderPayloadFileCopiesAndUnlinksInput(t *testing.T) {
	file, err := recorderPayloadFile(t.TempDir(), strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	assertReaderContents(t, file, "payload")
	assertPathMissing(t, file.Name())
}

func TestRecorderPayloadHelpersRejectOversizeInput(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), core.MaxRecorderPayloadBytes+1)
	if err := copyRecorderPayload(newRecorderTempFile(t), bytes.NewReader(payload)); err == nil {
		t.Fatal("oversized payload copy was accepted")
	}
	if _, err := readRecorderPayload(bytes.NewReader(payload)); err == nil {
		t.Fatal("oversized worker payload was accepted")
	}
}

func TestCopyRecorderPayloadReturnsReaderError(t *testing.T) {
	wantErr := errors.New("reader failed")
	file := newRecorderTempFile(t)
	if err := copyRecorderPayload(file, recorderErrorReader{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("payload copy error = %v, want %v", err, wantErr)
	}
}

func newRecorderTempFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

type recorderErrorReader struct{ err error }

func (r recorderErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestConfigureRecorderProcessPassesPrivateInputs(t *testing.T) {
	lock, input := createRecorderInputFiles(t)
	cmd := exec.Command("unused")
	configureRecorderProcess(cmd, lock, input)
	assertRecorderProcessConfiguration(t, cmd, lock, input)
}

func createRecorderInputFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	t.Cleanup(func() { _ = input.Close() })
	return lock, input
}

func assertRecorderProcessConfiguration(t *testing.T, cmd *exec.Cmd, lock, input *os.File) {
	t.Helper()
	processIsDetached := cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
	lockIsInherited := len(cmd.ExtraFiles) == 1 && cmd.ExtraFiles[0] == lock
	if cmd.Stdin != input {
		t.Fatal("recorder process did not receive its payload input")
	}
	if !processIsDetached {
		t.Fatal("recorder process is not detached")
	}
	if !lockIsInherited {
		t.Fatal("recorder process did not inherit its lock")
	}
	if !containsEnvironmentValue(cmd.Env, "DIU_RECORDING=1") {
		t.Fatal("recorder process did not set DIU_RECORDING")
	}
}

func containsEnvironmentValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestStartBackgroundRecorderRejectsInvalidDataDirectory(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Daemon.DataDir = filepath.Join(t.TempDir(), "file", "child")
	if err := os.WriteFile(filepath.Dir(config.Daemon.DataDir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startBackgroundRecorder(config, strings.NewReader("payload")); err == nil {
		t.Fatal("recorder accepted an invalid data directory")
	}
}
