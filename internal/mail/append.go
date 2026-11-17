package mail

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
)

// Append mengunggah satu pesan utuh (RFC822) ke mailbox — dipakai untuk
// menyimpan draft ke Drafts dan salinan pesan terkirim ke Sent.
func (cl *Client) Append(ctx context.Context, mailbox string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := cl.c.Append(mailbox, int64(len(data)), &imap.AppendOptions{})
	if _, err := cmd.Write(data); err != nil {
		return fmt.Errorf("APPEND %s: tulis data: %w", mailbox, err)
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("APPEND %s: %w", mailbox, err)
	}
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("APPEND %s: %w", mailbox, err)
	}
	return nil
}
