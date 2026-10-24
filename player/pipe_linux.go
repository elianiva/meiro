package player

import (
	"os"

	"golang.org/x/sys/unix"
)

// videoPipeSize is the buffer of the pipe a picture's frames come through,
// where the default holds 64 KiB, so a frame moves in fewer reads. It is
// the largest an unprivileged process may ask for by default.
const videoPipeSize = 1 << 20

// newVideoPipe returns a pipe for a picture's frames whose buffer holds
// videoPipeSize bytes. A system that caps pipes lower keeps the default,
// which works, only at more cost.
func newVideoPipe() (r, w *os.File, err error) {
	r, w, err = os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	if conn, err := w.SyscallConn(); err == nil {
		_ = conn.Control(func(fd uintptr) {
			_, _ = unix.FcntlInt(fd, unix.F_SETPIPE_SZ, videoPipeSize)
		})
	}
	return r, w, nil
}
