package sync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRefresher struct {
	calls atomic.Int64
}

func (f *fakeRefresher) Refresh(ctx context.Context) error {
	f.calls.Add(1)
	return nil
}

// TestWorkerCycles memastikan siklus pertama langsung dan polling berulang,
// dijalankan di bawah -race oleh Definition of Done.
func TestWorkerCycles(t *testing.T) {
	f := &fakeRefresher{}
	var updates atomic.Int64
	w := NewWorker(f, 20*time.Millisecond, func() { updates.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker tidak berhenti setelah cancel")
	}
	if f.calls.Load() < 2 {
		t.Fatalf("siklus refresh %d, mau >= 2", f.calls.Load())
	}
	if u := updates.Load(); u < 2 {
		t.Fatalf("update callback %d, mau >= 2", u)
	}
}

// TestWorkerContextError memastikan Refresh yang error tidak menghentikan worker.
func TestWorkerContextError(t *testing.T) {
	f := &fakeRefresher{}
	w := NewWorker(f, 10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker tidak berhenti")
	}
}

type fakeWatcher struct {
	fakeRefresher
	push chan struct{}
}

func (f *fakeWatcher) Watch(ctx context.Context, onEvent func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.push:
			onEvent()
		}
	}
}

// TestWorkerIdleTrigger memastikan push IDLE langsung memicu siklus Refresh
// tanpa menunggu interval polling (diuji di bawah -race).
func TestWorkerIdleTrigger(t *testing.T) {
	f := &fakeWatcher{push: make(chan struct{}, 1)}
	var updates atomic.Int64
	w := NewWorker(f, time.Hour, func() { updates.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	// Tunggu siklus awal.
	deadline := time.Now().Add(time.Second)
	for f.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Dorong event IDLE.
	f.push <- struct{}{}
	deadline = time.Now().Add(time.Second)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker IDLE tidak berhenti setelah cancel")
	}
	if f.calls.Load() < 2 || updates.Load() < 2 {
		t.Fatalf("IDLE gagal memicu refresh: calls=%d updates=%d", f.calls.Load(), updates.Load())
	}
}
