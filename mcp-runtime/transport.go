package mcpruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func buildTransport(server protocol.ServerConfig) sdk.Transport {
	if strings.TrimSpace(server.URL) != "" {
		client := server.HTTPClient
		if client == nil {
			client = &http.Client{}
		}
		if len(server.Headers) > 0 {
			clone := *client
			base := client.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			headers := make(http.Header)
			for name, value := range server.Headers {
				headers.Set(name, value)
			}
			clone.Transport = &headerTransport{base: base, headers: headers}
			client = &clone
		}
		if server.Type == "sse" {
			return &sdk.SSEClientTransport{Endpoint: server.URL, HTTPClient: client}
		}
		return &sdk.StreamableClientTransport{
			Endpoint: server.URL, HTTPClient: client,
			DisableStandaloneSSE: true,
		}
	}
	command := exec.Command(server.Command, server.Args...)
	command.Dir = server.Dir
	command.Env = os.Environ()
	for name, value := range server.Env {
		command.Env = append(command.Env, name+"="+value)
	}
	return &commandTransport{command: command, maxLineLength: server.MaxLineLength}
}

// CommandTransport does not expose the SDK's frame limit. Use IOTransport while
// retaining the stdio shutdown sequence: close stdin, wait, SIGTERM, then kill.
type commandTransport struct {
	command           *exec.Cmd
	maxLineLength     int
	terminateDuration time.Duration
}

func (transport *commandTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdout, err := transport.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := transport.command.StdinPipe()
	if err != nil {
		return nil, errors.Join(err, stdout.Close())
	}
	if err := transport.command.Start(); err != nil {
		return nil, errors.Join(err, stdin.Close(), stdout.Close())
	}
	duration := transport.terminateDuration
	if duration <= 0 {
		duration = 5 * time.Second
	}
	input := &commandInput{WriteCloser: stdin, command: transport.command, terminateDuration: duration}
	ioTransport := &sdk.IOTransport{
		// Wait closes stdout after the process exits; start shutdown through stdin.
		Reader: io.NopCloser(stdout), Writer: input, MaxLineLength: transport.maxLineLength,
	}
	connection, err := ioTransport.Connect(ctx)
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	return connection, nil
}

type commandInput struct {
	io.WriteCloser
	command           *exec.Cmd
	terminateDuration time.Duration
}

func (input *commandInput) Close() error {
	closeErr := input.WriteCloser.Close()
	finished := make(chan error, 1)
	go func() { finished <- input.command.Wait() }()
	wait := func() (error, bool) {
		timer := time.NewTimer(input.terminateDuration)
		defer timer.Stop()
		select {
		case err := <-finished:
			return errors.Join(closeErr, err), true
		case <-timer.C:
			return nil, false
		}
	}
	if err, exited := wait(); exited {
		return err
	}
	if err := input.command.Process.Signal(syscall.SIGTERM); err == nil {
		if err, exited := wait(); exited {
			return err
		}
	}
	killErr := input.command.Process.Kill()
	if err, exited := wait(); exited {
		return errors.Join(err, killErr)
	}
	return errors.Join(closeErr, killErr, fmt.Errorf("MCP subprocess did not exit after termination"))
}

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (transport *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	for name, values := range transport.headers {
		clone.Header[name] = append([]string(nil), values...)
	}
	return transport.base.RoundTrip(clone)
}
