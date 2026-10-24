//go:build !windows && !linux

package player

import "os"

// newVideoPipe returns a pipe for a picture's frames. macOS and the BSDs
// size a pipe's buffer themselves and offer no way to enlarge it.
func newVideoPipe() (r, w *os.File, err error) { return os.Pipe() }
