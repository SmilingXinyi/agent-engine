package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SmilingXinyi/agent-engine/cli"
	"github.com/SmilingXinyi/agent-engine/cli/protocol"
)

func main() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cli.Run(context.Background(), os.Args[1:], protocol.Options{Input: os.Stdin, Output: os.Stdout, Signals: signals}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
