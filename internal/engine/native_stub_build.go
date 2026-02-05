//go:build !silero

package engine

import "errors"

// ErrNativeUnavailable indicates the Silero engine is not compiled in.
var ErrNativeUnavailable = errors.New("engine: silero backend not available (build without -tags silero)")

// NativeAvailable reports that no native engine is compiled in.
func NativeAvailable() bool { return false }

// NewNativeEngine returns an error when built without the silero tag.
func NewNativeEngine(_ float64) (Engine, error) {
	return nil, ErrNativeUnavailable
}
