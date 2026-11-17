// Package sync menjalankan sinkronisasi email berkala di latar belakang.
// Worker tidak boleh menyentuh widget mygo: hasil dikirim lewat callback
// onUpdate, yang main.go bungkus dengan mygo Window.Update.
package sync

import (
	"context"
	"log"
	"time"
)

// Refresher adalah kemampuan yang dibutuhkan worker (dipenuhi app.Service).
type Refresher interface {
	Refresh(ctx context.Context) error
}

// Worker melakukan polling berkala dan memanggil onUpdate setelah tiap siklus.
type Worker struct {
	refresher Refresher
	interval  time.Duration
	onUpdate  func()
}

// NewWorker membuat worker; interval <= 0 diganti default 5 menit.
func NewWorker(r Refresher, interval time.Duration, onUpdate func()) *Worker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Worker{refresher: r, interval: interval, onUpdate: onUpdate}
}

// Run menjalankan polling sampai ctx dibatalkan. Blokir; panggil dalam goroutine.
// Siklus pertama langsung dieksekusi.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.cycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.cycle(ctx)
		}
	}
}

func (w *Worker) cycle(ctx context.Context) {
	if err := w.refresher.Refresh(ctx); err != nil {
		// Sinkronisasi gagal bukan alasan mati: catat, coba lagi nanti.
		log.Printf("sync: %v", err)
	}
	if w.onUpdate != nil {
		w.onUpdate()
	}
}
