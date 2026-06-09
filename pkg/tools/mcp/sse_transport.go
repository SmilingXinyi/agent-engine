package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/zendev-sh/goai/mcp"
)

// RelativeSSETransport supports legacy MCP SSE servers that announce relative message endpoints.
type RelativeSSETransport struct {
	URL        string
	Headers    map[string]string
	Endpoint   string
	HTTPClient *http.Client

	respBody      io.ReadCloser
	endpointReady chan struct{}

	mu        sync.Mutex
	onMessage func(mcp.JSONRPCMessage)
	onClose   func()
	onError   func(error)
}

func NewRelativeSSETransport(sseURL string, headers map[string]string) *RelativeSSETransport {
	return &RelativeSSETransport{URL: sseURL, Headers: headers}
}

func (transport *RelativeSSETransport) OnMessage(fn func(mcp.JSONRPCMessage)) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.onMessage = fn
}

func (transport *RelativeSSETransport) OnClose(fn func()) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.onClose = fn
}

func (transport *RelativeSSETransport) OnError(fn func(error)) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.onError = fn
}

func (transport *RelativeSSETransport) Start(ctx context.Context) error {
	transport.endpointReady = make(chan struct{})
	client := transport.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, transport.URL, nil)
	if err != nil {
		return fmt.Errorf("mcp: create SSE request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	for name, value := range transport.Headers {
		request.Header.Set(name, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("mcp: SSE connection: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return fmt.Errorf("mcp: SSE connection failed: HTTP %d", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		_ = response.Body.Close()
		return fmt.Errorf("mcp: expected text/event-stream, got %q", contentType)
	}
	transport.respBody = response.Body
	go transport.readSSE(bufio.NewReader(response.Body))
	return nil
}

func (transport *RelativeSSETransport) Send(ctx context.Context, msg mcp.JSONRPCMessage) error {
	transport.mu.Lock()
	endpoint := transport.Endpoint
	transport.mu.Unlock()
	if endpoint == "" {
		if transport.endpointReady != nil {
			select {
			case <-transport.endpointReady:
				transport.mu.Lock()
				endpoint = transport.Endpoint
				transport.mu.Unlock()
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if endpoint == "" {
			return errors.New("mcp: SSE endpoint not yet discovered from server")
		}
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal message: %w", err)
	}
	client := transport.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp: create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range transport.Headers {
		request.Header.Set(name, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("mcp: send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(response.Body)
		return fmt.Errorf("mcp: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	contentType := response.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var responseMessage mcp.JSONRPCMessage
		if err := json.NewDecoder(response.Body).Decode(&responseMessage); err == nil && responseMessage.JSONRPC == "2.0" {
			transport.dispatchMessage(responseMessage)
		}
	}
	return nil
}

func (transport *RelativeSSETransport) Close() error {
	transport.mu.Lock()
	body := transport.respBody
	transport.respBody = nil
	transport.mu.Unlock()
	if body != nil {
		_ = body.Close()
	}
	return nil
}

func (transport *RelativeSSETransport) readSSE(reader *bufio.Reader) {
	for {
		eventType, data, err := readSSEEvent(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				transport.dispatchClose()
			} else {
				transport.dispatchError(err)
			}
			return
		}
		if data == "" {
			continue
		}
		if eventType == "endpoint" {
			transport.setEndpoint(data)
			continue
		}

		var msg mcp.JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			transport.dispatchError(err)
			continue
		}
		transport.dispatchMessage(msg)
	}
}

func (transport *RelativeSSETransport) setEndpoint(endpoint string) {
	resolvedEndpoint := transport.resolveEndpoint(endpoint)
	transport.mu.Lock()
	first := transport.Endpoint == ""
	transport.Endpoint = resolvedEndpoint
	transport.mu.Unlock()
	if first && transport.endpointReady != nil {
		close(transport.endpointReady)
	}
}

func (transport *RelativeSSETransport) resolveEndpoint(endpoint string) string {
	parsedEndpoint, err := url.Parse(endpoint)
	if err == nil && parsedEndpoint.IsAbs() {
		return endpoint
	}
	baseURL, err := url.Parse(transport.URL)
	if err != nil {
		return endpoint
	}
	return baseURL.ResolveReference(parsedEndpoint).String()
}

func (transport *RelativeSSETransport) dispatchMessage(msg mcp.JSONRPCMessage) {
	transport.mu.Lock()
	fn := transport.onMessage
	transport.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func (transport *RelativeSSETransport) dispatchClose() {
	transport.mu.Lock()
	fn := transport.onClose
	transport.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (transport *RelativeSSETransport) dispatchError(err error) {
	transport.mu.Lock()
	fn := transport.onError
	transport.mu.Unlock()
	if fn != nil {
		fn(err)
	}
}

func readSSEEvent(reader *bufio.Reader) (eventType string, data string, err error) {
	var dataLines []string
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			if strings.HasPrefix(line, "event:") {
				eventType = stripSSEValue(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, stripSSEValue(strings.TrimPrefix(line, "data:")))
			}
		}
		if readErr != nil {
			if len(dataLines) > 0 {
				return eventType, strings.Join(dataLines, "\n"), nil
			}
			return "", "", readErr
		}
		if line == "" {
			if len(dataLines) > 0 {
				return eventType, strings.Join(dataLines, "\n"), nil
			}
			continue
		}
	}
}

func stripSSEValue(value string) string {
	if strings.HasPrefix(value, " ") {
		return value[1:]
	}
	return value
}
