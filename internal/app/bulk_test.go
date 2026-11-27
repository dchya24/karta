package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"karta/internal/mail"
	"karta/internal/store"
)

// seedBulk menyiapkan Service dengan 1 akun, folder INBOX/Archive/Trash,
// dan 3 pesan di INBOX (UID 10/11/12). Mengembalikan ID folder dan pesan.
func seedBulk(t *testing.T) (*Service, inboxArchiveTrash, []int) {
	t.Helper()
	svc := newTestService(t)
	accID, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := svc.db.EnsureFolder(int64(accID), "INBOX", "inbox")
	archive, _ := svc.db.EnsureFolder(int64(accID), "Archive", "archive")
	trash, _ := svc.db.EnsureFolder(int64(accID), "Trash", "trash")
	var ids []int
	for i, uid := range []int64{10, 11, 12} {
		id, err := svc.db.UpsertMessage(&store.MessageHeader{
			FolderID: int(inbox), UID: int(uid), Subject: "bulk " + string(rune('a'+i)),
			Sender: "Budi <b@x.id>", SenderAddr: "b@x.id",
			Date: "2026-11-17T01:00:00Z", Unread: true,
		}, &store.MessageBody{PlainText: "isi"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, int(id))
	}
	return svc, inboxArchiveTrash{inbox, archive, trash}, ids
}

type inboxArchiveTrash struct{ inbox, archive, trash int64 }

// TestArchiveManyOrderAndCalls: ArchiveMany memanggil MOVE per-ID sesuai
// urutan input, semua pesan pindah ke folder Archive, dan error nil.
func TestArchiveManyOrderAndCalls(t *testing.T) {
	svc, f, ids := seedBulk(t)
	stub := &actionStubIMAP{nextDestUID: 99}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	// Urutan input sengaja acak: urutan eksekusi harus mengikuti input.
	in := []int{ids[2], ids[0], ids[1]}
	if err := svc.ArchiveMany(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(stub.moveCalls) != 3 {
		t.Fatalf("move calls %+v, mau 3", stub.moveCalls)
	}
	// UID input 12,10,11 → urutan MOVE harus sama.
	wantUIDs := []uint32{12, 10, 11}
	for i, mv := range stub.moveCalls {
		if mv.src != "INBOX" || mv.dst != "Archive" || mv.uid != wantUIDs[i] {
			t.Fatalf("move[%d] = %+v, mau src=INBOX dst=Archive uid=%d", i, mv, wantUIDs[i])
		}
	}
	msgs, err := svc.ListMessages(int(f.archive), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("pesan di Archive %+v, mau 3", msgs)
	}
	if n, _ := svc.ListMessages(int(f.inbox), ""); len(n) != 0 {
		t.Fatalf("INBOX harus kosong: %+v", n)
	}
}

// TestDeleteManyAggregatesErrors: DeleteMany tetap memproses ID setelah
// kegagalan, mengumpulkan error per-ID sebagai *MultiError dengan konteks
// "pesan N", dan sukses penuh mengembalikan nil.
func TestDeleteManyAggregatesErrors(t *testing.T) {
	svc, f, ids := seedBulk(t)
	stub := &actionStubIMAP{nextDestUID: 99}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	// ID 999 tidak ada → gagal; dua ID valid tetap diproses (urutan terjaga).
	err := svc.DeleteMany(context.Background(), []int{ids[1], 999, ids[2]})
	if err == nil {
		t.Fatal("harus error karena ada ID yang tidak ada")
	}
	var me *MultiError
	if !errors.As(err, &me) {
		t.Fatalf("harus *MultiError, dapat %T", err)
	}
	if len(me.Errs) != 1 || !strings.Contains(me.Errs[0].Error(), "pesan 999") {
		t.Fatalf("error agregat salah: %v", me.Errs)
	}
	msgs, _ := svc.ListMessages(int(f.trash), "")
	if len(msgs) != 2 {
		t.Fatalf("pesan di Trash %+v, mau 2 (ID valid tetap diproses)", msgs)
	}

	// Semua valid → nil.
	if err := svc.DeleteMany(context.Background(), []int{ids[0]}); err != nil {
		t.Fatalf("DeleteMany sukses harus nil, dapat %v", err)
	}
	if msgs, _ = svc.ListMessages(int(f.trash), ""); len(msgs) != 3 {
		t.Fatalf("pesan di Trash %+v, mau 3", msgs)
	}

	// DeleteMany pada pesan yang sudah di Trash = hapus permanen + expunge.
	if err := svc.DeleteMany(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	if len(stub.deleteCalls) != 3 {
		t.Fatalf("expunge calls %+v, mau 3", stub.deleteCalls)
	}
	if msgs, _ = svc.ListMessages(int(f.trash), ""); len(msgs) != 0 {
		t.Fatalf("Trash harus kosong: %+v", msgs)
	}
}

// TestArchiveManyEmpty: daftar kosong tidak error dan tidak memanggil IMAP.
func TestArchiveManyEmpty(t *testing.T) {
	svc, _, _ := seedBulk(t)
	stub := &actionStubIMAP{}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()
	if err := svc.ArchiveMany(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteMany(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.moveCalls) != 0 || len(stub.deleteCalls) != 0 {
		t.Fatal("daftar kosong tidak boleh memicu operasi IMAP")
	}
}
