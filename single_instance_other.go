//go:build !windows

package main

func isAlreadyRunning() bool {
	return false
}

func restoreExistingWindow() {}

func ensureSingleInstance() (func(), bool) {
	return func() {}, true
}
