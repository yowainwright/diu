package safefs

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func WriteFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return err
	}
	defer func() { err = CloseWithError(err, root, "failed to close root") }()
	unchanged, err := unchangedRegularFile(root, name, data, mode)
	if err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	return replaceRegularFile(root, name, data, mode)
}

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

func CloseWithError(current error, closer io.Closer, context string) error {
	closeErr := closer.Close()
	if current != nil {
		return current
	}
	if closeErr == nil {
		return current
	}
	if context == "" {
		return closeErr
	}
	return fmt.Errorf("%s: %w", context, closeErr)
}

func SHA256(path string) (digest string, err error) {
	file, err := OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer func() {
		err = CloseWithError(err, file, "")
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Stat(path string) (info os.FileInfo, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = CloseWithError(err, root, "failed to close root")
	}()

	return root.Stat(name)
}

func Lstat(path string) (info os.FileInfo, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = CloseWithError(err, root, "failed to close root")
	}()

	return root.Lstat(name)
}

func ReadFile(path string) (data []byte, err error) {
	root, name, err := openRootFor(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = CloseWithError(err, root, "failed to close root")
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
