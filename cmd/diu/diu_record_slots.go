package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

func acquireRecorderSlot(dataDir string) (*os.File, int, error) {
	if err := os.MkdirAll(dataDir, core.OwnerDirectoryMode); err != nil {
		return nil, 0, err
	}
	for slot := range core.MaxRecorderWorkers {
		file, err := tryRecorderSlot(core.RecorderSlotPath(dataDir, slot))
		hasResult := file != nil || err != nil
		if hasResult {
			return file, slot, err
		}
	}
	return nil, 0, nil
}

func tryRecorderSlot(path string) (*os.File, error) {
	flags := os.O_CREATE | os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	file, err := safefs.OpenFile(path, flags, core.PrivateFileMode)
	if err != nil {
		return nil, err
	}
	if err := validateRecorderSlot(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := lockFallbackRecorder(file); err != nil {
		_ = file.Close()
		if isFallbackLockContention(err) {
			return nil, nil
		}
		return nil, err
	}
	return file, nil
}

func validateRecorderSlot(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	isOwned := ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
	isValid := info.Mode().IsRegular() && isOwned
	if !isValid {
		return fmt.Errorf("invalid recorder slot")
	}
	return file.Chmod(core.PrivateFileMode)
}

func inheritedRecorderSlot(dataDir string, slot int) (*os.File, error) {
	file := os.NewFile(3, "recorder slot")
	if file == nil {
		return nil, fmt.Errorf("missing recorder slot")
	}
	if err := verifyInheritedRecorderSlot(file, dataDir, slot); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func verifyInheritedRecorderSlot(file *os.File, dataDir string, slot int) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	expected, err := os.Lstat(core.RecorderSlotPath(dataDir, slot))
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected) {
		return fmt.Errorf("recorder slot does not match inherited descriptor")
	}
	if err := validateRecorderSlot(file); err != nil {
		return err
	}
	return lockFallbackRecorder(file)
}
