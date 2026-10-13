package mcpruntime

import (
	"net/http"
	"os"
	"os/exec"
	"strings"

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
	return &sdk.CommandTransport{Command: command}
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
