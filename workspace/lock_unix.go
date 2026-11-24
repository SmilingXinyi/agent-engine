//go:build darwin || linux

package workspace

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func acquireLock(path string) (*os.File, error) {
	if err := regularOrMissing(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("workspace is already open; close the other session and try again: %w", err)
		}
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	return file, nil
}
