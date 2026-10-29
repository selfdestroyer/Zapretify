package platform

// EncryptedDNS tells whether the system resolver uses DNS over HTTPS/TLS, which the provider
// can neither read nor spoof. Checked is false where the state cannot be determined.
type EncryptedDNS struct {
	Checked bool   `json:"checked"`
	Enabled bool   `json:"enabled"`
	Via     string `json:"via,omitempty"` // profile name or servers
}
