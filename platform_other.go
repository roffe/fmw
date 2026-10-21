//go:build !android

package fmw

// queuedReads is how many of the mixer's reads oto's driver queues ahead of
// the device: none here.
const queuedReads = 0

func platformReady() error { return nil }
