package main

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	publicAPIRate  = 2.0
	publicAPIBurst = 30.0
)

type apiBucket struct {
	tokens float64
	last   time.Time
}

type apiRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]apiBucket
	lastGC  time.Time
}

func newAPIRateLimiter() *apiRateLimiter {
	return &apiRateLimiter{buckets: make(map[string]apiBucket)}
}

func (l *apiRateLimiter) allow(key string, now time.Time) (remaining int, retry time.Duration, allowed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		bucket = apiBucket{tokens: publicAPIBurst, last: now}
	}
	bucket.tokens = min(publicAPIBurst, bucket.tokens+now.Sub(bucket.last).Seconds()*publicAPIRate)
	bucket.last = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		allowed = true
	} else {
		retry = time.Duration(math.Ceil((1-bucket.tokens)/publicAPIRate*1000)) * time.Millisecond
	}
	l.buckets[key] = bucket
	remaining = max(0, int(bucket.tokens))

	if l.lastGC.IsZero() || now.Sub(l.lastGC) >= 5*time.Minute {
		for address, entry := range l.buckets {
			if now.Sub(entry.last) > 10*time.Minute {
				delete(l.buckets, address)
			}
		}
		l.lastGC = now
	}
	return
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
			return real.String()
		}
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return net.ParseIP(forwarded).String()
		}
	}
	if ip != nil {
		return ip.String()
	}
	return host
}

func (l *apiRateLimiter) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		remaining, retry, allowed := l.allow(requestIP(r), time.Now())
		w.Header().Set("RateLimit-Limit", "120")
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("RateLimit-Policy", `"qday-public";q=120;w=60`)
		if !allowed {
			seconds := max(1, int(math.Ceil(retry.Seconds())))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "public API rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
