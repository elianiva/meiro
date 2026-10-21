//go:build !darwin && !linux && !(windows && (amd64 || arm64))

package systemmedia

import "errors"

// New is unsupported on platforms without a system media-control adapter.
func New(Controls) (Session, error) {
	return nil, errors.New("system media controls are not supported on this platform")
}
