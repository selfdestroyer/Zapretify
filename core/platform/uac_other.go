//go:build !windows

package platform

func IsAdmin() bool {
	return false
}

func RunAsAdmin(args ...string) error {
	return nil
}
