//go:build !darwin && !linux && !windows

package mcpcontrol

import (
	"context"
	"errors"
	"net"
)

var errUnsupported = errors.New("Meiro AI control is unavailable on this platform")

func listen(context.Context) (net.Listener, error) { return nil, errUnsupported }
func dial(context.Context) (net.Conn, error)       { return nil, errUnsupported }
