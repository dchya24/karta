package mail

import (
	"context"
	"errors"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ErrIdleUnsupported menandakan server IMAP tidak mendukung ekstensi IDLE.
var ErrIdleUnsupported = errors.New("server IMAP tidak mendukung IDLE")

// IdleMailbox membuka koneksi IMAP khusus IDLE pada mailbox (umumnya INBOX),
// memanggil onEvent saat server mengirim notifikasi EXISTS/EXPUNGE, hingga ctx
// dibatalkan atau koneksi terputus.
func IdleMailbox(ctx context.Context, cfg IMAPConfig, mailbox string, onEvent func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	notify := make(chan struct{}, 1)
	signal := func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
	cl, err := DialIMAPWithHandler(cfg, &imapclient.UnilateralDataHandler{
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages != nil {
				signal()
			}
		},
		Expunge: func(uint32) { signal() },
	})
	if err != nil {
		return err
	}
	defer cl.Close()

	if !cl.c.Caps().Has(imap.CapIdle) && !cl.c.Caps().Has(imap.CapIMAP4rev2) {
		return ErrIdleUnsupported
	}
	if _, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("SELECT IDLE %s: %w", mailbox, err)
	}
	cmd, err := cl.c.Idle()
	if err != nil {
		return fmt.Errorf("IDLE %s: %w", mailbox, err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	for {
		select {
		case <-notify:
			if onEvent != nil {
				onEvent()
			}
		case err := <-waitCh:
			return err
		case <-ctx.Done():
			if err := cmd.Close(); err != nil {
				_ = cl.c.Close()
			}
			<-waitCh
			return ctx.Err()
		}
	}
}
