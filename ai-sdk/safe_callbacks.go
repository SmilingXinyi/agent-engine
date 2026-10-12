package ai

import (
	"context"
	"fmt"
)

func callbackPanicError(name string, recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("%s panicked: %w", name, err)
	}
	return fmt.Errorf("%s panicked: %v", name, recovered)
}

func callRequestHook(ctx context.Context, hook RequestHook, info RequestInfo) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = callbackPanicError("request hook", recovered)
		}
	}()
	return hook(ctx, info)
}

func callResponseHook(ctx context.Context, hook ResponseHook, info ResponseInfo) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = callbackPanicError("response hook", recovered)
		}
	}()
	return hook(ctx, info)
}

func callEventSink(ctx context.Context, sink EventSink, event StreamEvent) (err error) {
	if sink == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = callbackPanicError("event sink", recovered)
		}
	}()
	return sink.OnEvent(ctx, event)
}
