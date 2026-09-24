package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestRecorderSlotOwnedByEffectiveUser(t *testing.T) {
	tests := []struct {
		name         string
		uid          uint32
		effectiveUID int64
		isOwned      bool
	}{
		{name: "same user", uid: 12, effectiveUID: 12, isOwned: true},
		{name: "different user", uid: 12, effectiveUID: 13},
		{name: "negative effective UID", effectiveUID: -1},
		{name: "effective UID above uint32", effectiveUID: 1 << 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recorderSlotOwnedByEffectiveUser(test.uid, test.effectiveUID); got != test.isOwned {
				t.Fatalf("owner match = %t, want %t", got, test.isOwned)
			}
		})
	}
}

func TestAcquireRecorderSlotEnforcesWorkerLimit(t *testing.T) {
	dir := t.TempDir()
	locks := acquireAllRecorderSlots(t, dir)
	defer closeRecorderSlots(t, locks)
	lock, slot, err := acquireRecorderSlot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		t.Fatal("exhausted slot acquisition returned a lock")
	}
	if slot != 0 {
		t.Fatalf("exhausted slot acquisition = (%v, %d, %v)", lock, slot, err)
	}
}

func acquireAllRecorderSlots(t *testing.T, dir string) []*os.File {
	t.Helper()
	locks := make([]*os.File, 0, core.MaxRecorderWorkers)
	for want := range core.MaxRecorderWorkers {
		lock, slot, err := acquireRecorderSlot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if lock == nil {
			t.Fatalf("slot %d acquisition returned no lock", want)
		}
		if slot != want {
			t.Fatalf("slot acquisition = (%v, %d, %v), want slot %d", lock, slot, err, want)
		}
		locks = append(locks, lock)
	}
	return locks
}

func closeRecorderSlots(t *testing.T, locks []*os.File) {
	t.Helper()
	for _, lock := range locks {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestAcquireRecorderSlotReportsDirectoryFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acquireRecorderSlot(filepath.Join(file, "child")); err == nil {
		t.Fatal("acquiring a slot below a file must fail")
	}
}

func TestTryRecorderSlotRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "slot")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := tryRecorderSlot(link); err == nil {
		t.Fatal("symlinked recorder slot must be rejected")
	}
}

func TestValidateRecorderSlotRejectsHardLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slot")
	if err := os.WriteFile(path, nil, core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+"-alias"); err != nil {
		t.Fatal(err)
	}
	assertRecorderSlotInvalid(t, path)
}

func assertRecorderSlotInvalid(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := validateRecorderSlot(file); err == nil {
		t.Fatal("invalid recorder slot was accepted")
	}
}

func TestVerifyInheritedRecorderSlotChecksFileIdentity(t *testing.T) {
	dir := t.TempDir()
	slotPath := core.RecorderSlotPath(dir, 0)
	if err := os.WriteFile(slotPath, nil, core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(slotPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := verifyInheritedRecorderSlot(file, dir, 0); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyInheritedRecorderSlotRejectsDifferentFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(core.RecorderSlotPath(dir, 0), nil, core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "other")
	if err := os.WriteFile(filePath, nil, core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := verifyInheritedRecorderSlot(file, dir, 0); err == nil {
		t.Fatal("different inherited file was accepted")
	}
}
