package safefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Stat(path string) (info os.FileInfo, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := root.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close root: %w", closeErr)
		}
	}()

	return root.Stat(name)
}

func Lstat(path string) (info os.FileInfo, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := root.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close root: %w", closeErr)
		}
	}()

	return root.Lstat(name)
}

func ReadFile(path string) (data []byte, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := root.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close root: %w", closeErr)
		}
	}()

	return root.ReadFile(name)
}

func OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}

	file, openErr := root.OpenFile(name, flag, perm)
	closeErr := root.Close()
	if openErr != nil {
		return nil, openErr
	}
	if closeErr != nil {
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("failed to close root: %w; additionally failed to close file: %v", closeErr, err)
		}
		return nil, fmt.Errorf("failed to close root: %w", closeErr)
	}
	return file, nil
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
