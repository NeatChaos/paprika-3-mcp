// Package cachestore persists the recipe cache in a single object with generation preconditions.
package cachestore

import (
	"context"
	"errors"
)

var (
	ErrNotFound     = errors.New("cache object not found")
	ErrPrecondition = errors.New("cache object generation precondition failed")
)

// Store holds one cache object. Load returns the data and its generation. Save writes the data only
// if the object's generation is still ifGeneration, where 0 means the object must not exist yet,
// and returns the new generation.
type Store interface {
	Load(ctx context.Context) ([]byte, int64, error)
	Save(ctx context.Context, data []byte, ifGeneration int64) (int64, error)
}
