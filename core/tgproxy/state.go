package tgproxy

import (
	"sort"
	"sync"
	"time"
)

var (
	stats         Stats
	blacklist     = make(map[dcKey]time.Time)
	blMu          sync.Mutex
	failUntil     = make(map[dcKey]time.Time)
	fuMu          sync.Mutex
	ipFailUntil   = make(map[string]time.Time)
	ipFuMu        sync.Mutex
	frontingUntil time.Time
	frontingMu    sync.Mutex
)

func setCooldown(k dcKey) {
	fuMu.Lock()
	failUntil[k] = time.Now().Add(dcFailCooldown)
	fuMu.Unlock()
}

func inCooldown(k dcKey) bool {
	fuMu.Lock()
	t, ok := failUntil[k]
	fuMu.Unlock()
	return ok && time.Now().Before(t)
}

func clearCooldown(k dcKey) {
	fuMu.Lock()
	delete(failUntil, k)
	fuMu.Unlock()
}

func setIPCooldown(ip string) {
	if ip == "" {
		return
	}
	ipFuMu.Lock()
	ipFailUntil[ip] = time.Now().Add(ipFailCooldown)
	ipFuMu.Unlock()
	globalPool.discardTarget(ip)
}

func inIPCooldown(ip string) bool {
	if ip == "" {
		return false
	}
	ipFuMu.Lock()
	t, ok := ipFailUntil[ip]
	if ok && !time.Now().Before(t) {
		delete(ipFailUntil, ip)
		ok = false
	}
	ipFuMu.Unlock()
	return ok
}

func clearIPCooldown(ip string) {
	if ip == "" {
		return
	}
	ipFuMu.Lock()
	delete(ipFailUntil, ip)
	ipFuMu.Unlock()
}

func setFrontingActive() {
	frontingMu.Lock()
	frontingUntil = time.Now().Add(frontingCooldown)
	frontingMu.Unlock()
}

func clearFrontingActive() {
	frontingMu.Lock()
	frontingUntil = time.Time{}
	frontingMu.Unlock()
}

func frontingActive() bool {
	frontingMu.Lock()
	active := time.Now().Before(frontingUntil)
	frontingMu.Unlock()
	return active
}

func setBlacklisted(k dcKey) {
	blMu.Lock()
	blacklist[k] = time.Now().Add(dcBlacklistTTL)
	blMu.Unlock()
}

func isBlacklisted(dc int, media bool) bool {
	k := dcKey{DC: dc, IsMedia: media}
	blMu.Lock()
	t, ok := blacklist[k]
	if ok && !time.Now().Before(t) {
		delete(blacklist, k)
		ok = false
	}
	blMu.Unlock()
	return ok
}

func sortedDCMap(m map[int]string) []dcMapItem {
	items := make([]dcMapItem, 0, len(m))
	for dc, ip := range m {
		items = append(items, dcMapItem{dc: dc, ip: ip})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].dc < items[j].dc
	})
	return items
}
