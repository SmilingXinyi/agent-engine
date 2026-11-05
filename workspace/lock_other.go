//go:build !darwin && !linux

package workspace

import (
	"fmt"
	"os"
)

func acquireLock(string) (*os.File, error) {
	return nil, fmt.Errorf("workspace currently supports only macOS and Linux")
}
