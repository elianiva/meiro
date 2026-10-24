package player

import (
	"os"
	"syscall"
)

// videoPipeSize is the buffer of the pipe a picture's frames come through.
// os.Pipe takes the system's default of a few kilobytes, through which
// reading a 1080p frame took 5.3 ms of CPU; through 1 MiB it took 1.35 ms,
// and 8 MiB did no better.
const videoPipeSize = 1 << 20

// newVideoPipe returns a pipe for a picture's frames whose buffer holds
// videoPipeSize bytes. Neither end is inherited: exec hands the child its
// own copy of the end it is given.
func newVideoPipe() (r, w *os.File, err error) {
	var rh, wh syscall.Handle
	if err := syscall.CreatePipe(&rh, &wh, nil, videoPipeSize); err != nil {
		return nil, nil, os.NewSyscallError("CreatePipe", err)
	}
	return os.NewFile(uintptr(rh), "|0"), os.NewFile(uintptr(wh), "|1"), nil
}
