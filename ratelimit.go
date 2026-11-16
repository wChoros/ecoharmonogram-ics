package main

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// RateLimiter to token bucket na klienta z ograniczoną liczbą śledzonych adresów,
// więc zużycie pamięci ma stały sufit niezależnie od ruchu.
type RateLimiter struct {
	mu         sync.Mutex
	perSec     float64
	burst      float64
	maxClients int
	clients    map[netip.Prefix]*bucket
	now        func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(perHour float64, burst, maxClients int) *RateLimiter {
	return &RateLimiter{
		perSec:     perHour / 3600,
		burst:      float64(burst),
		maxClients: maxClients,
		clients:    make(map[netip.Prefix]*bucket),
		now:        time.Now,
	}
}

// Allow zwraca, czy żądanie przechodzi, a jeśli nie, po jakim czasie spróbować ponownie.
func (rl *RateLimiter) Allow(key netip.Prefix) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.clients[key]
	if !ok {
		if len(rl.clients) >= rl.maxClients {
			rl.pruneLocked(now)
			if len(rl.clients) >= rl.maxClients {
				return false, time.Minute // tabela pełna: odrzucamy nowych klientów
			}
		}
		b = &bucket{tokens: rl.burst, last: now}
		rl.clients[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.perSec
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rl.perSec * float64(time.Second))
	return false, wait
}

// Prune usuwa klientów, których kubełek zdążył się już w pełni napełnić,
// bo i tak są nieodróżnialni od nowych.
func (rl *RateLimiter) Prune() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.pruneLocked(rl.now())
}

func (rl *RateLimiter) pruneLocked(now time.Time) {
	full := time.Duration(rl.burst / rl.perSec * float64(time.Second))
	for k, b := range rl.clients {
		if now.Sub(b.last) >= full {
			delete(rl.clients, k)
		}
	}
}

func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.clients)
}

// clientKey zwraca klucz limitu: pełny adres IPv4 albo całą sieć /64 dla IPv6
// (jeden klient zwykle dostaje całą /64, więc rotacja adresów nie obchodzi limitu).
func clientKey(remoteAddr string) netip.Prefix {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Prefix{}
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32)
	}
	p, _ := addr.WithZone("").Prefix(64)
	return p
}
