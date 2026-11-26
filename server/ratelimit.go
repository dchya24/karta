package main

import (
	"sync"
	"time"
)

// rateLimiter membatasi ~limit permintaan per window (fixed window) per IP.
// Sederhana dan stateful di memori; layanan relay ini single-instance.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration

	buckets map[string]*rateBucket
}

// rateBucket menghitung permintaan satu IP dalam satu window.
type rateBucket struct {
	count   int
	resetAt time.Time
}

// newRateLimiter membuat limiter yang mengizinkan limit permintaan per window.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*rateBucket),
	}
}

// allow melaporkan apakah permintaan dari key (IP) boleh lewat. Window baru
// dimulai otomatis setelah window sebelumnya lewat.
func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.pruneLocked()

	now := time.Now()
	b, ok := r.buckets[key]
	if !ok || now.After(b.resetAt) {
		r.buckets[key] = &rateBucket{count: 1, resetAt: now.Add(r.window)}
		return true
	}
	b.count++
	return b.count <= r.limit
}

// pruneLocked membuang bucket kedaluwarsa agar peta tidak tumbuh tanpa batas.
// Dipanggil saat peta mencapai ambang tertentu.
func (r *rateLimiter) pruneLocked() {
	const maxBuckets = 4096
	if len(r.buckets) < maxBuckets {
		return
	}
	now := time.Now()
	for k, b := range r.buckets {
		if now.After(b.resetAt) {
			delete(r.buckets, k)
		}
	}
}
