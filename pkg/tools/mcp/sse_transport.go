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
	endpointOnce  sync.Once
	endpointErr   error
	terminalErr   error
	pending       map[string]any

	mu        sync.Mutex
	onMessage func(mcp.JSONRPCMessage)
	onClose   func()
	onError   func(error)
}

func NewRelativeSSETransport(sseURL string, headers map[string]string) *RelativeSSETransport {
	clonedHeaders := make(map[string]string, len(headers))
	for name, value := range headers {
		clonedHeaders[name] = value
	}
	return &RelativeSSETransport{URL: sseURL, Headers: clonedHeaders}
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
	transport.mu.Lock()
	transport.Endpoint = ""
	transport.endpointErr = nil
	transport.terminalErr = nil
	transport.pending = make(map[string]any)
	transport.endpointReady = make(chan struct{})
	transport.endpointOnce = sync.Once{}
	transport.mu.Unlock()
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
		responseErr := fmt.Errorf("mcp: SSE connection failed: HTTP %d", response.StatusCode)
		return errors.Join(responseErr, closeResponseBody(response.Body))
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		responseErr := fmt.Errorf("mcp: expected text/event-stream, got %q", contentType)
		return errors.Join(responseErr, closeResponseBody(response.Body))
	}
	transport.mu.Lock()
	transport.respBody = response.Body
	transport.mu.Unlock()
	go transport.readSSE(bufio.NewReader(response.Body))
	return nil
}

func (transport *RelativeSSETransport) Send(ctx context.Context, msg mcp.JSONRPCMessage) (sendErr error) {
	transport.mu.Lock()
	endpoint := transport.Endpoint
	endpointErr := transport.endpointErr
	terminalErr := transport.terminalErr
	endpointReady := transport.endpointReady
	transport.mu.Unlock()
	if terminalErr != nil {
		return terminalErr
	}
	if endpoint == "" && endpointErr == nil {
		if endpointReady != nil {
			select {
			case <-endpointReady:
				transport.mu.Lock()
				endpoint = transport.Endpoint
				endpointErr = transport.endpointErr
				transport.mu.Unlock()
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if endpointErr != nil {
			return endpointErr
		}
		if endpoint == "" {
			return errors.New("mcp: SSE endpoint not yet discovered from server")
		}
	} else if endpointErr != nil {
		return endpointErr
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

	pendingKey, tracksResponse, err := transport.trackPending(msg)
	if err != nil {
		return err
	}
	if tracksResponse {
		defer func() {
			if sendErr != nil {
				transport.removePending(pendingKey)
			}
		}()
		if done := ctx.Done(); done != nil {
			go func() {
				<-done
				transport.removePending(pendingKey)
			}()
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("mcp: send request: %w", err)
	}
	defer func() {
		sendErr = errors.Join(sendErr, closeResponseBody(response.Body))
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return fmt.Errorf("mcp: HTTP %d: read response body: %w", response.StatusCode, readErr)
		}
		return fmt.Errorf("mcp: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	contentType := response.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var responseMessage mcp.JSONRPCMessage
		if err := json.NewDecoder(response.Body).Decode(&responseMessage); err != nil {
			return fmt.Errorf("mcp: decode JSON response: %w", err)
		}
		if responseMessage.JSONRPC != "2.0" {
			return fmt.Errorf("mcp: invalid JSON-RPC response version %q", responseMessage.JSONRPC)
		}
		if tracksResponse && pendingKey != pendingKeyForID(responseMessage.ID) {
			return fmt.Errorf("mcp: JSON-RPC response id does not match request")
		}
		transport.dispatchMessage(responseMessage)
	}
	return nil
}

func (transport *RelativeSSETransport) Close() error {
	closeErr := errors.New("mcp: SSE transport closed")
	transport.failEndpoint(fmt.Errorf("%w before endpoint discovery", closeErr))
	transport.failPending(closeErr)
	transport.mu.Lock()
	body := transport.respBody
	transport.respBody = nil
	transport.mu.Unlock()
	if body != nil {
		return closeResponseBody(body)
	}
	return nil
}

func (transport *RelativeSSETransport) readSSE(reader *bufio.Reader) {
	for {
		eventType, data, err := readSSEEvent(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				connectionErr := errors.New("mcp: SSE connection closed")
				transport.failEndpoint(fmt.Errorf("%w before endpoint discovery", connectionErr))
				transport.failPending(connectionErr)
				transport.dispatchClose()
			} else {
				connectionErr := fmt.Errorf("mcp: read SSE stream: %w", err)
				transport.failEndpoint(fmt.Errorf("mcp: SSE endpoint discovery failed: %w", err))
				transport.failPending(connectionErr)
				transport.dispatchError(connectionErr)
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
			messageErr := fmt.Errorf("mcp: decode SSE message: %w", err)
			transport.failPending(messageErr)
			transport.dispatchError(messageErr)
			return
		}
		if msg.JSONRPC != "2.0" {
			messageErr := fmt.Errorf("mcp: invalid SSE JSON-RPC version %q", msg.JSONRPC)
			transport.failPending(messageErr)
			transport.dispatchError(messageErr)
			return
		}
		transport.dispatchMessage(msg)
	}
}

func (transport *RelativeSSETransport) setEndpoint(endpoint string) {
	resolvedEndpoint, err := transport.resolveEndpoint(endpoint)
	if err != nil {
		transport.failEndpoint(err)
		transport.failPending(err)
		transport.dispatchError(err)
		return
	}
	transport.mu.Lock()
	if transport.endpointErr != nil {
		transport.mu.Unlock()
		return
	}
	first := transport.Endpoint == ""
	transport.Endpoint = resolvedEndpoint
	transport.mu.Unlock()
	if first {
		transport.signalEndpointReady()
	}
}

func (transport *RelativeSSETransport) resolveEndpoint(endpoint string) (string, error) {
	parsedEndpoint, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("mcp: invalid SSE message endpoint: %w", err)
	}
	baseURL, err := url.Parse(transport.URL)
	if err != nil {
		return "", fmt.Errorf("mcp: invalid SSE base URL: %w", err)
	}
	resolvedEndpoint := baseURL.ResolveReference(parsedEndpoint)
	if !strings.EqualFold(baseURL.Scheme, resolvedEndpoint.Scheme) || !strings.EqualFold(baseURL.Host, resolvedEndpoint.Host) {
		return "", fmt.Errorf("mcp: SSE message endpoint must use the same origin as the SSE URL")
	}
	return resolvedEndpoint.String(), nil
}

func (transport *RelativeSSETransport) failEndpoint(err error) {
	transport.mu.Lock()
	if transport.Endpoint != "" {
		transport.mu.Unlock()
		return
	}
	if transport.endpointErr == nil {
		transport.endpointErr = err
	}
	transport.mu.Unlock()
	transport.signalEndpointReady()
}

func (transport *RelativeSSETransport) signalEndpointReady() {
	transport.mu.Lock()
	ready := transport.endpointReady
	transport.mu.Unlock()
	if ready != nil {
		transport.endpointOnce.Do(func() { close(ready) })
	}
}

func (transport *RelativeSSETransport) dispatchMessage(msg mcp.JSONRPCMessage) {
	transport.mu.Lock()
	if transport.terminalErr != nil {
		transport.mu.Unlock()
		return
	}
	if msg.ID != nil {
		delete(transport.pending, pendingKeyForID(msg.ID))
	}
	fn := transport.onMessage
	transport.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func (transport *RelativeSSETransport) trackPending(msg mcp.JSONRPCMessage) (string, bool, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.terminalErr != nil {
		return "", false, transport.terminalErr
	}
	if msg.ID == nil || msg.Method == "" {
		return "", false, nil
	}
	key := pendingKeyForID(msg.ID)
	if transport.pending == nil {
		transport.pending = make(map[string]any)
	}
	transport.pending[key] = msg.ID
	return key, true, nil
}

func (transport *RelativeSSETransport) removePending(key string) {
	transport.mu.Lock()
	delete(transport.pending, key)
	transport.mu.Unlock()
}

func (transport *RelativeSSETransport) failPending(err error) {
	if err == nil {
		return
	}
	transport.mu.Lock()
	if transport.terminalErr == nil {
		transport.terminalErr = err
	}
	ids := make([]any, 0, len(transport.pending))
	for key, id := range transport.pending {
		ids = append(ids, id)
		delete(transport.pending, key)
	}
	fn := transport.onMessage
	transport.mu.Unlock()
	if fn == nil {
		return
	}
	for _, id := range ids {
		fn(mcp.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
			RPCError: &mcp.JSONRPCError{
				Code:    mcp.ErrCodeServerError,
				Message: err.Error(),
			},
		})
	}
}

func pendingKeyForID(id any) string {
	return fmt.Sprint(id)
}

func closeResponseBody(body io.Closer) error {
	if body == nil {
		return nil
	}
	if err := body.Close(); err != nil {
		return fmt.Errorf("mcp: close response body: %w", err)
	}
	return nil
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
