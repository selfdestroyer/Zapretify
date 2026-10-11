package tgproxy

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type pooledWS struct {
	Conn    *websocket.Conn
	Created time.Time
}

type wsPoolKey struct {
	DC       int
	IsMedia  bool
	TargetIP string
}

type wsPool struct {
	mu        sync.Mutex
	idle      map[wsPoolKey][]pooledWS
	refilling map[wsPoolKey]bool
}

var globalPool = newWSPool()

func newWSPool() *wsPool {
	return &wsPool{
		idle:      make(map[wsPoolKey][]pooledWS),
		refilling: make(map[wsPoolKey]bool),
	}
}

func (p *wsPool) get(cfg *Config, key dcKey, targetIP string, domains []string) *websocket.Conn {
	poolKey := wsPoolKey{DC: key.DC, IsMedia: key.IsMedia, TargetIP: targetIP}
	now := time.Now()
	for {
		p.mu.Lock()
		bucket := p.idle[poolKey]
		if len(bucket) == 0 {
			p.scheduleRefill(cfg, poolKey, domains)
			p.mu.Unlock()
			atomic.AddInt64(&stats.poolMisses, 1)
			return nil
		}
		item := bucket[0]
		p.idle[poolKey] = bucket[1:]
		p.scheduleRefill(cfg, poolKey, domains)
		p.mu.Unlock()

		if now.Sub(item.Created) > wsPoolMaxAge {
			_ = item.Conn.Close()
			continue
		}
		atomic.AddInt64(&stats.poolHits, 1)
		return item.Conn
	}
}

func (p *wsPool) scheduleRefill(cfg *Config, key wsPoolKey, domains []string) {
	if cfg.PoolSize <= 0 || p.refilling[key] {
		return
	}
	p.refilling[key] = true
	go p.refill(cfg, key, domains)
}

func (p *wsPool) refill(cfg *Config, key wsPoolKey, domains []string) {
	defer func() {
		p.mu.Lock()
		delete(p.refilling, key)
		p.mu.Unlock()
	}()

	for {
		p.mu.Lock()
		cur := len(p.idle[key])
		p.mu.Unlock()
		if cur >= cfg.PoolSize {
			return
		}
		useFronting := frontingActive()
		if inIPCooldown(key.TargetIP) && !useFronting {
			return
		}
		var conn *websocket.Conn
		var err error
		if useFronting {
			conn, _, err = wsConnectFronting(key.TargetIP, domains, poolConnectTimeout)
		} else {
			conn, _, err = wsConnect(key.TargetIP, domains, poolConnectTimeout)
		}
		if err != nil {
			return
		}
		p.mu.Lock()
		if inIPCooldown(key.TargetIP) && !useFronting {
			p.mu.Unlock()
			_ = conn.Close()
			return
		}
		p.idle[key] = append(p.idle[key], pooledWS{Conn: conn, Created: time.Now()})
		p.mu.Unlock()
	}
}

func (p *wsPool) discardTarget(targetIP string) {
	if targetIP == "" {
		return
	}
	var stale []pooledWS
	p.mu.Lock()
	for key, bucket := range p.idle {
		if key.TargetIP != targetIP {
			continue
		}
		stale = append(stale, bucket...)
		delete(p.idle, key)
	}
	p.mu.Unlock()

	for _, s := range stale {
		_ = s.Conn.Close()
	}
}

func (p *wsPool) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, bucket := range p.idle {
		for _, s := range bucket {
			_ = s.Conn.Close()
		}
	}
	p.idle = make(map[wsPoolKey][]pooledWS)
	p.refilling = make(map[wsPoolKey]bool)
}
