//go:build !windows

package platform

// Known shell folders exist only on Windows.
func DesktopDir() string                 { return "" }
func StartMenuProgramsDir() string       { return "" }
func PublicDesktopDir() string           { return "" }
func CommonStartMenuProgramsDir() string { return "" }
