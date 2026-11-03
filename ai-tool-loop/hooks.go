package aitoolloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func newID(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(random[:]), nil
}

func notify[T interface{ Clone() T }](ctx context.Context, name string, hook func(context.Context, T) error, info T) (err error) {
	if hook == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s hook panicked: %v", name, recovered)
		}
	}()
	if err := hook(ctx, info.Clone()); err != nil {
		return fmt.Errorf("%s hook: %w", name, err)
	}
	return nil
}
