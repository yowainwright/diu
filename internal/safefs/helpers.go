package safefs

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func unchangedRegularFile(root *os.Root, name string, data []byte, mode os.FileMode) (bool, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing to replace non-regular file: %s", name)
	}
	current, err := root.ReadFile(name)
	unchanged := bytes.Equal(current, data) && info.Mode().Perm() == mode.Perm()
	return unchanged, err
}

func replaceRegularFile(root *os.Root, name string, data []byte, mode os.FileMode) error {
	temporary := ".diu-" + rand.Text()
	defer func() { _ = root.Remove(temporary) }()
	if err := writeReplacement(root, temporary, data, mode); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

func writeReplacement(root *os.Root, name string, data []byte, mode os.FileMode) (err error) {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { err = CloseWithError(err, file, "failed to close replacement") }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	return file.Sync()
}

func openRootFor(path string) (*os.Root, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("path cannot be empty")
	}

	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	name := filepath.Base(cleanPath)
	if name == string(filepath.Separator) {
		name = "."
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	return root, name, nil
}
