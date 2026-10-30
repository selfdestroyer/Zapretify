package main

import (
	"sync"
	"time"

	"zapretify/core/platform"
)

// vpnStatusTTL keeps /api/status cheap: the UI polls it every 1.5 s, the VPN state changes rarely.
const vpnStatusTTL = 3 * time.Second

var vpnStatus struct {
	sync.Mutex
	at   time.Time
	list []string
}

// activeVPN returns the VPN/TUN adapters for the main screen warning ("Name (description)").
func activeVPN() []string {
	vpnStatus.Lock()
	defer vpnStatus.Unlock()
	if time.Since(vpnStatus.at) >= vpnStatusTTL {
		vpnStatus.list, vpnStatus.at = platform.ActiveVPNAdapters(), time.Now()
		if vpnStatus.list == nil {
			vpnStatus.list = []string{}
		}
	}
	return vpnStatus.list
}
