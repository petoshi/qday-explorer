package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestAPIRateLimiter(t *testing.T) {
	limiter := newAPIRateLimiter()
	now := time.Unix(1_000, 0)
	for i := 0; i < int(publicAPIBurst); i++ {
		if _, _, ok := limiter.allow("192.0.2.1", now); !ok {
			t.Fatalf("request %d rejected before burst was exhausted", i+1)
		}
	}
	if _, retry, ok := limiter.allow("192.0.2.1", now); ok || retry != 500*time.Millisecond {
		t.Fatalf("exhausted bucket returned ok=%v retry=%v", ok, retry)
	}
	if _, _, ok := limiter.allow("192.0.2.1", now.Add(500*time.Millisecond)); !ok {
		t.Fatal("refilled request was rejected")
	}
}

func TestRequestIP(t *testing.T) {
	request := httptest.NewRequest("GET", "https://explorer.pqday.com/api/status", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Real-IP", "203.0.113.7")
	if got := requestIP(request); got != "203.0.113.7" {
		t.Fatalf("got proxy client %q", got)
	}
	request.RemoteAddr = "198.51.100.9:1234"
	if got := requestIP(request); got != "198.51.100.9" {
		t.Fatalf("trusted forwarded address from a public peer: %q", got)
	}
}

func TestAPIRateLimitResponse(t *testing.T) {
	limiter := newAPIRateLimiter()
	handler := limiter.handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < int(publicAPIBurst); i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		request.RemoteAddr = "192.0.2.44:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("request %d returned HTTP %d", i+1, response.Code)
		}
		if response.Header().Get("RateLimit-Limit") != "120" {
			t.Fatal("response omitted public rate limit")
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.RemoteAddr = "192.0.2.44:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted limit returned HTTP %d", response.Code)
	} else if seconds, err := strconv.Atoi(response.Header().Get("Retry-After")); err != nil || seconds < 1 {
		t.Fatalf("invalid Retry-After %q", response.Header().Get("Retry-After"))
	}

	// Static pages are not charged against the public API bucket.
	request = httptest.NewRequest(http.MethodGet, "/blocks", nil)
	request.RemoteAddr = "192.0.2.44:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("static route returned HTTP %d after API limit", response.Code)
	}
}
