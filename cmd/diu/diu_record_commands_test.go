package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestRecorderSlotArgumentValidation(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  int
		valid bool
	}{
		{name: "zero", args: []string{"0"}, valid: true},
		{name: "last slot", args: []string{"3"}, want: 3, valid: true},
		{name: "missing", valid: false},
		{name: "extra", args: []string{"0", "1"}, valid: false},
		{name: "not a number", args: []string{"bad"}, valid: false},
		{name: "negative", args: []string{"-1"}, valid: false},
		{name: "past limit", args: []string{"4"}, valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := recorderSlotArgument(test.args)
			isValid := err == nil
			slotMatches := !test.valid || got == test.want
			invalidResult := isValid != test.valid || !slotMatches
			if invalidResult {
				t.Fatalf("slot argument %v = (%d, %v)", test.args, got, err)
			}
		})
	}
}

func TestRecorderCommandsAreHiddenAndValidateArguments(t *testing.T) {
	commands := []*command{newRecorderSupervisorCommand(), newRecorderWorkerCommand()}
	for _, cmd := range commands {
		if !cmd.IsHidden {
			t.Fatalf("internal command %q is visible", cmd.Use)
		}
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Fatalf("internal command %q accepted missing slot", cmd.Use)
		}
	}
}

func TestWithRecorderSlotLoadsConfigBeforeDescriptor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(testConfigPath()), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testConfigPath(), []byte("{"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	err := withRecorderSlot([]string{"0"}, func(*core.Config, *os.File, int) error { return nil })
	if err == nil {
		t.Fatal("invalid config must prevent opening an inherited descriptor")
	}
}

func TestWithRecorderSlotUsesInheritedDescriptor(t *testing.T) {
	config := setupTestHomeConfig(t)
	marker := filepath.Join(t.TempDir(), "worker-ran")
	lock := createRecorderSlotFile(t, config)
	runRecorderSlotProcess(t, lock, marker)
	assertRecorderSlotMarker(t, marker)
}

func testConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "diu", "config.json")
}

func createRecorderSlotFile(t *testing.T, config *core.Config) *os.File {
	t.Helper()
	if err := os.MkdirAll(config.Daemon.DataDir, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(core.RecorderSlotPath(config.Daemon.DataDir, 0), os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestRecorderSlotProcessHelper(t *testing.T) {
	if os.Getenv("DIU_TEST_RECORDER_SLOT") != "1" {
		return
	}
	t.Setenv("HOME", os.Getenv("DIU_TEST_RECORDER_SLOT_HOME"))
	err := withRecorderSlot([]string{"0"}, func(_ *core.Config, _ *os.File, slot int) error {
		return os.WriteFile(os.Getenv("DIU_TEST_RECORDER_SLOT_MARKER"), []byte("slot=0"), core.PrivateFileMode)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runRecorderSlotProcess(t *testing.T, lock *os.File, marker string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecorderSlotProcessHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_RECORDER_SLOT=1", "DIU_TEST_RECORDER_SLOT_HOME="+os.Getenv("HOME"), "DIU_TEST_RECORDER_SLOT_MARKER="+marker)
	cmd.ExtraFiles = []*os.File{lock}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recorder slot command failed: %v\n%s", err, output)
	}
}

func assertRecorderSlotMarker(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	markerMatches := string(data) == "slot=0"
	if !markerMatches {
		t.Fatalf("recorder slot marker = %q, %v", data, err)
	}
}
