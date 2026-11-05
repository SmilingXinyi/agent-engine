//go:build darwin || linux

package workspace

import (
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
		return nil, fmt.Errorf("workspace is already open or cannot be locked: %w", err)
	}
	return file, nil
}
