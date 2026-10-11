package tgproxy

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Host                         string
	Port                         int
	SecretHex                    string
	FakeTLSDomain                string
	DCMap                        map[int]string
	DCPool                       map[int][]string
	FallbackCFProxy              bool
	FallbackCFProxyPriority      bool
	FallbackCFProxyDomains       []string
	FallbackCFProxyWorkerDomains []string
	FallbackCFProxyActive        string
	FallbackCFProxyPerDCActive   map[int]string
	BufKB                        int
	PoolSize                     int
	MaxConns                     int
	ForceTestDC                  bool
	cfproxyMu                    sync.RWMutex
	cfproxyFailUntil             map[string]time.Time
}

func (c *Config) hasCFProxyWorkerDomains() bool {
	return len(c.FallbackCFProxyWorkerDomains) > 0
}

func (c *Config) hasCFProxyDomains() bool {
	return len(c.FallbackCFProxyDomains) > 0
}

func (c *Config) cfproxyWorkerDomainsForTry() []string {
	return c.FallbackCFProxyWorkerDomains
}

func (c *Config) cfproxyDomainsForTry(dc int) []string {
	c.cfproxyMu.RLock()
	defer c.cfproxyMu.RUnlock()
	return c.FallbackCFProxyDomains
}

func (c *Config) markCFProxyDomainFailed(domain string, cooldown time.Duration) bool {
	c.cfproxyMu.Lock()
	defer c.cfproxyMu.Unlock()
	if c.cfproxyFailUntil == nil {
		c.cfproxyFailUntil = make(map[string]time.Time)
	}
	until, exists := c.cfproxyFailUntil[domain]
	now := time.Now()
	c.cfproxyFailUntil[domain] = now.Add(cooldown)
	return !exists || now.After(until)
}

func (c *Config) promoteCFProxyDomain(dc int, domain string) {
	c.cfproxyMu.Lock()
	defer c.cfproxyMu.Unlock()
	if c.FallbackCFProxyPerDCActive == nil {
		c.FallbackCFProxyPerDCActive = make(map[int]string)
	}
	c.FallbackCFProxyPerDCActive[dc] = domain
	c.FallbackCFProxyActive = domain
}

type Stats struct {
	connectionsTotal  int64
	connectionsActive int64
	connectionsWS     int64
	connectionsTCP    int64
	connectionsCF     int64
	connectionsFront  int64
	connectionsBad    int64
	wsErrors          int64
	bytesUp           int64
	bytesDown         int64
	poolHits          int64
	poolMisses        int64
}

func (s *Stats) summary() string {
	hits := atomic.LoadInt64(&s.poolHits)
	misses := atomic.LoadInt64(&s.poolMisses)
	poolTotal := hits + misses
	poolS := "n/a"
	if poolTotal > 0 {
		poolS = fmt.Sprintf("%d/%d", hits, poolTotal)
	}
	return fmt.Sprintf(
		"total=%d active=%d ws=%d tcp_fb=%d cf=%d front=%d bad=%d err=%d pool=%s up=%s down=%s",
		atomic.LoadInt64(&s.connectionsTotal),
		atomic.LoadInt64(&s.connectionsActive),
		atomic.LoadInt64(&s.connectionsWS),
		atomic.LoadInt64(&s.connectionsTCP),
		atomic.LoadInt64(&s.connectionsCF),
		atomic.LoadInt64(&s.connectionsFront),
		atomic.LoadInt64(&s.connectionsBad),
		atomic.LoadInt64(&s.wsErrors),
		poolS,
		humanBytes(atomic.LoadInt64(&s.bytesUp)),
		humanBytes(atomic.LoadInt64(&s.bytesDown)),
	)
}

type handshakeInfo struct {
	DC         int
	IsMedia    bool
	ProtoTag   []byte
	ClientDecI []byte
}

type dcKey struct {
	DC      int
	IsMedia bool
}

type dcMapItem struct {
	dc int
	ip string
}
