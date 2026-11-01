package middleware

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"qslate/gateway/internal/platform/httpx"
)

type bucket struct {
	count int
	reset time.Time
}

// Limiter is per gateway instance. A bounded map prevents unbounded IP cardinality.
// RemoteAddr is trusted; arbitrary client forwarding headers are not.
type Limiter struct {
	mu              sync.Mutex
	buckets         map[string]bucket
	limit, capacity int
	window          time.Duration
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{buckets: make(map[string]bucket), limit: limit, capacity: 4096, window: window}
}

func (l *Limiter) allow(key string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.buckets[key]
	if !exists || !now.Before(b.reset) {
		if len(l.buckets) >= l.capacity {
			for ip, entry := range l.buckets {
				if !now.Before(entry.reset) {
					delete(l.buckets, ip)
				}
			}
			if !exists && len(l.buckets) >= l.capacity {
				return false, int(l.window.Seconds())
			}
		}
		b = bucket{reset: now.Add(l.window)}
	}
	if b.count >= l.limit {
		return false, max(1, int(b.reset.Sub(now).Seconds())+1)
	}
	b.count++
	l.buckets[key] = b
	return true, 0
}

func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		allowed, retry := l.allow(ip, time.Now())
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			httpx.Error(w, 429, "rate_limited", "too many authentication attempts")
			return
		}
		next.ServeHTTP(w, r)
	})
}
