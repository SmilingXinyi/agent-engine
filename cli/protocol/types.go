// Package protocol defines command runtime dependencies.
package protocol

import (
	"io"
	"os"
)

// Options supplies streams and an optional signal channel. The caller registers
// signals; nil Signals disables signal-channel handling. Chat owns and closes
// Input, but Output remains caller-owned.
type Options struct {
	Input   io.ReadCloser
	Output  io.Writer
	Signals <-chan os.Signal
}
