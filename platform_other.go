//go:build !darwin

package main

import "zapretify/core/zapret"

// defaultEngine runs winws.exe as a child process.
func defaultEngine() zapret.Engine { return zapret.GetRunner() }

// prepareAppDir: the data already sits next to the executable.
func prepareAppDir(string) {}
