//go:build !windows && !darwin

package platform

// CheckEncryptedDNS is not implemented on this platform.
func CheckEncryptedDNS() EncryptedDNS { return EncryptedDNS{} }
