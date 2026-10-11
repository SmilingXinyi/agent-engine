package aisdk

import (
	"context"
	"time"
)

// RequestIDMetadataKey is the conventional metadata key used to correlate
// one model request across loop and provider hooks.
const RequestIDMetadataKey = "request_id"

// RequestInfo describes one model call before it is sent to the Provider.
type RequestInfo struct {
	Provider  string
	Request   ModelRequest
	RequestID string
	Timestamp time.Time
}

// ResponseInfo describes the outcome of one model call.
type ResponseInfo struct {
	Provider  string
	Request   ModelRequest
	RequestID string
	Response  ModelResponse
	Duration  time.Duration
	Error     error
}

// RequestHook observes a model call before it reaches the Provider.
type RequestHook func(context.Context, RequestInfo) error

// ResponseHook observes a model call after the Provider responds.
type ResponseHook func(context.Context, ResponseInfo) error

// Hooks describes the observational lifecycle around one model call. Both
// slices support multiple callbacks; there is no agent loop to control.
type Hooks struct {
	OnRequest  []RequestHook
	OnResponse []ResponseHook
}
