package aisdk

import "context"

// Provider maps the common ModelRequest and ModelResponse format to one AI
// service. Implementations without native incremental output can synthesize
// StreamEvents from a Generate result using EmitModelResponse.
type Provider interface {
	Name() string
	Generate(ctx context.Context, request ModelRequest) (ModelResponse, error)
	Stream(ctx context.Context, request ModelRequest, sink EventSink) (ModelResponse, error)
}
