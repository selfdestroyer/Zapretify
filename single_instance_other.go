//go:build !windows

package main

func ensureSingleInstance() (func(), bool) {
	return func() {}, true
}
