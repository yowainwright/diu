package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/yowainwright/diu/internal/safefs"
)

func removeWrapperDelegates(wrapperDir string) (err error) {
	root, err := os.OpenRoot(wrapperDir)
	if err != nil {
		return err
	}
	defer func() { err = safefs.CloseWithError(err, root, "close wrapper directory") }()
	info, err := root.Lstat(".diu-delegates")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to remove non-directory delegation cache")
	}
	return removeDelegateDirectories(root)
}

func removeDelegateDirectories(parent *os.Root) (err error) {
	root, err := parent.OpenRoot(".diu-delegates")
	if err != nil {
		return err
	}
	defer func() { err = safefs.CloseWithError(err, root, "close delegation cache") }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, entry := range entries {
		if isDelegateDirectory(entry) {
			cleanupErr = errors.Join(cleanupErr, removeDelegateDirectory(root, entry.Name()))
		}
	}
	return errors.Join(cleanupErr, removeEmptyDirectory(parent, ".diu-delegates"))
}

func isDelegateDirectory(entry os.DirEntry) bool {
	isDirectory := entry.IsDir()
	hasDigestLength := len(entry.Name()) == 64
	isCandidate := isDirectory && hasDigestLength
	if !isCandidate {
		return false
	}
	_, err := hex.DecodeString(entry.Name())
	return err == nil
}

func removeDelegateDirectory(parent *os.Root, name string) (err error) {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer func() { err = safefs.CloseWithError(err, root, "close delegate directory") }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, entry := range entries {
		cleanupErr = errors.Join(cleanupErr, removeGeneratedDelegate(root, entry))
	}
	return errors.Join(cleanupErr, removeEmptyDirectory(parent, name))
}

func removeGeneratedDelegate(root *os.Root, entry os.DirEntry) error {
	if !entry.Type().IsRegular() {
		return nil
	}
	content, err := root.ReadFile(entry.Name())
	if err != nil {
		return err
	}
	prefix := "#!/bin/bash\n# DIU delegated command\nexec "
	hasMarker := strings.HasPrefix(string(content), prefix)
	forwardsArguments := strings.HasSuffix(string(content), " \"$@\"\n")
	isGenerated := hasMarker && forwardsArguments
	if !isGenerated {
		return nil
	}
	return root.Remove(entry.Name())
}

func removeEmptyDirectory(root *os.Root, name string) error {
	err := root.Remove(name)
	notEmpty := errors.Is(err, syscall.ENOTEMPTY)
	stillExists := errors.Is(err, syscall.EEXIST)
	preservingFiles := notEmpty || stillExists
	if preservingFiles {
		return nil
	}
	return err
}
