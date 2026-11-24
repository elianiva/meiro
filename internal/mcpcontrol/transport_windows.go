//go:build windows

package mcpcontrol

import (
	"context"
	"errors"
	"net"
	"os/user"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func endpoint() (path, sid string, err error) {
	account, err := user.Current()
	if err != nil {
		return "", "", err
	}
	return `\\.\pipe\meiro-mcp-` + account.Uid, account.Uid, nil
}

func listen(context.Context) (net.Listener, error) {
	path, sid, err := endpoint()
	if err != nil {
		return nil, err
	}
	return listenPipe(path, sid)
}

func listenPipe(path, sid string) (net.Listener, error) {
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "O:" + sid + "D:P(A;;GA;;;" + sid + ")", InputBufferSize: requestLimit, OutputBufferSize: requestLimit})
}

func dial(ctx context.Context) (net.Conn, error) {
	path, sid, err := endpoint()
	if err != nil {
		return nil, err
	}
	return dialPipe(ctx, path, sid)
}

func dialPipe(ctx context.Context, path, sid string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, path, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL)
	if err != nil {
		return nil, err
	}
	if err := verifyPipeOwner(conn, sid); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func verifyPipeOwner(conn net.Conn, sid string) error {
	handle, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("could not verify the Meiro pipe owner")
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(handle.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	if owner == nil || owner.String() != sid {
		return errors.New("the Meiro pipe must belong to your user")
	}
	return nil
}
