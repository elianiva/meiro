//go:build !unix

package main

// cpuSeconds is not measured where there is no getrusage: the soak and idle
// tests report zero CPU there.
func cpuSeconds() float64 { return 0 }
