package platform

import (
	"os"
	"testing"
)

// Opt-in: reads the profiles installed on this Mac.
func TestLiveCheckEncryptedDNS(t *testing.T) {
	if os.Getenv("ZAPRETIFY_LIVE_TESTS") == "" {
		t.Skip("set ZAPRETIFY_LIVE_TESTS=1")
	}
	t.Logf("%+v", CheckEncryptedDNS())
}
