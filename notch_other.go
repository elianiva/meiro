//go:build !darwin

package main

import "github.com/egoist/mygo"

func readNotchCutout() mygo.Rectangle      { return mygo.Rectangle{} }
func prepareNotchWindow(*mygo.Window)      {}
func setNotchFocusable(*mygo.Window, bool) {}
func focusNotchWindow(*mygo.Window)        {}
func releaseNotchFocus(*mygo.Window)       {}
