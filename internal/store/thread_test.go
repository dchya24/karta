package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func testFolder(t *testing.T, db *DB) int64 {
	t.Helper()
	accID, err := db.AddAccount("thread@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNormalizeMessageID(t *testing.T) {
	cases := map[string]string{
		"<ABC@X.id>":    "abc@x.id",
		"  <a.B@x>  ":   "a.b@x",
		"a@x":           "a@x",
		"":              "",
		"   ":           "",
		"<MiXeD@Co.id>": "mixed@co.id",
		"<a@x> <b@y>":   "a@x> <b@y", // pemanggil splitMessageIDs, bukan ini
	}
	for in, want := range cases {
		if got := NormalizeMessageID(in); got != want {
			t.Fatalf("NormalizeMessageID(%q)=%q mau %q", in, got, want)
		}
	}
}

func TestNormalizeThreadSubject(t *testing.T) {
	cases := map[string]string{
		"Laporan mingguan":                 "laporan mingguan",
		"Re: Laporan mingguan":             "laporan mingguan",
		"RE: Re: laporan mingguan":         "laporan mingguan",
		"Fwd:  Laporan   mingguan ":        "laporan mingguan",
		"FW: Fw: RE: laporan mingguan":     "laporan mingguan",
		"Re[2]: Laporan mingguan":          "laporan mingguan",
		"Fwd[12]: Laporan mingguan":        "laporan mingguan",
		"Restart: layanan":                 "restart: layanan", // bukan prefix reply
		"Reply-to-all jangan di-strip":     "reply-to-all jangan di-strip",
		"Re tanpa titik dua":               "re tanpa titik dua",
		"":                                 "",
		"Re:":                              "",
		"   Spasi    berlebihan\t di sini": "spasi berlebihan di sini",
	}
	for in, want := range cases {
		if got := NormalizeThreadSubject(in); got != want {
			t.Fatalf("NormalizeThreadSubject(%q)=%q mau %q", in, got, want)
		}
	}
}

func TestThreadSeedKey(t *testing.T) {
	// Rantai References menang: kunci = root rantai dinormalisasi.
	if got := ThreadSeedKey(1, "<c@x>", "<b@x>", "<a@x> <b@x>", "Re: topik"); got != "a@x" {
		t.Fatalf("seed refs=%q", got)
	}
	// In-Reply-To saja dipakai bila References kosong.
	if got := ThreadSeedKey(1, "", "<b@x>", "", "Re: topik"); got != "b@x" {
		t.Fatalf("seed irt=%q", got)
	}
	// Tanpa header reply sama sekali: fallback subject terikat folder.
	want := "s1:rapat pagi"
	if got := ThreadSeedKey(1, "<a@x>", "", "", "Re:   Rapat  Pagi"); got != want {
		t.Fatalf("seed subject=%q mau %q", got, want)
	}
	// Folder berbeda → kunci berbeda (thread per-folder).
	if got := ThreadSeedKey(2, "", "", "", "Rapat pagi"); got != "s2:rapat pagi" {
		t.Fatalf("seed folder lain=%q", got)
	}
	// Tanpa identitas apa pun: kosong (singleton di regroup).
	if got := ThreadSeedKey(1, "", "", "", ""); got != "" {
		t.Fatalf("seed kosong=%q", got)
	}
}

func TestRegroupReferencesChain(t *testing.T) {
	db := openTestDB(t)
	folder := testFolder(t, db)
	seed := func(uid int, mid, irt, refs, subj, date string) int64 {
		t.Helper()
		id, err := db.UpsertMessage(&MessageHeader{
			FolderID: int(folder), UID: uid, MessageID: mid, InReplyTo: irt,
			Refs: refs, Subject: subj, Date: date,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Rantai: root A ← B (in_reply_to) ← C (references lengkap), plus pesan
	// mandiri D dan thread subject-fallback E/F.
	a := seed(1, "<a@x>", "", "", "Rencana rilis", "2026-11-17T01:00:00Z")
	b := seed(2, "<b@x>", "<a@x>", "", "Re: Rencana rilis", "2026-11-17T02:00:00Z")
	c := seed(3, "<c@x>", "<b@x>", "<a@x> <b@x>", "Re: Rencana rilis", "2026-11-17T03:00:00Z")
	d := seed(4, "<d@x>", "", "", "Catatan rapat", "2026-11-17T04:00:00Z")
	e := seed(5, "", "", "", "Newsletter mingguan", "2026-11-17T05:00:00Z")
	f := seed(6, "", "", "", "RE: newsletter   mingguan", "2026-11-17T06:00:00Z")
	_ = a

	if err := db.RegroupThreads(folder); err != nil {
		t.Fatal(err)
	}
	threads, err := db.ListThreadSummaries(folder, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 3 {
		t.Fatalf("thread %+v, mau 3 baris", threads)
	}
	// Terbaru dulu: F (thread newsletter, terdiri E+F), D, C (thread rencana,
	// terdiri A+B+C).
	if threads[0].Subject != "RE: newsletter   mingguan" || threads[0].ThreadCount != 2 ||
		!threads[0].IsThreadLatest {
		t.Fatalf("baris 0: %+v", threads[0])
	}
	if threads[1].Subject != "Catatan rapat" || threads[1].ThreadCount != 1 {
		t.Fatalf("baris 1: %+v", threads[1])
	}
	if threads[2].Subject != "Re: Rencana rilis" || threads[2].ThreadCount != 3 {
		t.Fatalf("baris 2: %+v", threads[2])
	}
	// Klik thread membuka pesan terbaru: baris thread = pesan terbaru.
	if threads[0].UID != 6 || threads[2].UID != 3 {
		t.Fatalf("pesan terbaru thread salah: %+v", threads)
	}
	// Semua anggota thread rencana punya thread_id sama.
	getThread := func(id int64) string {
		var s string
		if err := db.QueryRow(`SELECT thread_id FROM messages WHERE id=?`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	ta, tb, tc := getThread(a), getThread(b), getThread(c)
	if ta == "" || ta != tb || tb != tc {
		t.Fatalf("rantai tidak terkunci: %q %q %q", ta, tb, tc)
	}
	te, tf := getThread(e), getThread(f)
	if te == "" || te != tf {
		t.Fatalf("fallback subject tidak menyatu: %q %q", te, tf)
	}
	if ta == te || ta == getThread(d) || te == getThread(d) {
		t.Fatalf("thread berbeda menyatu: %q %q", ta, te)
	}
	// Regroup idempoten: jalankan ulang, hasil tetap.
	before := threads
	if err := db.RegroupThreads(folder); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListThreadSummaries(folder, "")
	if len(after) != len(before) {
		t.Fatalf("regroup kedua mengubah jumlah thread: %d vs %d", len(after), len(before))
	}
	for i := range after {
		if after[i].ThreadID != before[i].ThreadID || after[i].UID != before[i].UID {
			t.Fatalf("regroup kedua mengubah baris %d: %+v vs %+v", i, after[i], before[i])
		}
	}
}

func TestRegroupOrderIndependence(t *testing.T) {
	// Reply yang masuk lebih dulu (root belum ada di folder) tetap bergabung
	// saat root menyusul — hasil regroup tidak tergantung urutan insert.
	build := func(inserts []int) string {
		db := openTestDB(t)
		folder := testFolder(t, db)
		type msg struct {
			uid            int
			mid, irt, subj string
		}
		all := []msg{
			{1, "<root@x>", "", "Topik utama"},
			{2, "<r1@x>", "<root@x>", "Re: Topik utama"},
			{3, "<r2@x>", "<r1@x>", "Re: Topik utama"},
		}
		byUID := map[int]msg{}
		for _, m := range all {
			byUID[m.uid] = m
		}
		for _, uid := range inserts {
			m := byUID[uid]
			if _, err := db.UpsertMessage(&MessageHeader{
				FolderID: int(folder), UID: m.uid, MessageID: m.mid, InReplyTo: m.irt,
				Subject: m.subj, Date: fmt.Sprintf("2026-11-17T0%d:00:00Z", m.uid),
			}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.RegroupThreads(folder); err != nil {
			t.Fatal(err)
		}
		threads, err := db.ListThreadSummaries(folder, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(threads) != 1 {
			t.Fatalf("insert %v: thread %+v, mau 1", inserts, threads)
		}
		return threads[0].ThreadID
	}
	orders := [][]int{{1, 2, 3}, {3, 2, 1}, {2, 3, 1}, {3, 1, 2}}
	want := build(orders[0])
	for _, o := range orders[1:] {
		if got := build(o); got != want {
			t.Fatalf("urutan %v menghasilkan thread %q, mau %q", o, got, want)
		}
	}
}

func TestRegroupPerFolder(t *testing.T) {
	db := openTestDB(t)
	accID, err := db.AddAccount("pf@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := db.EnsureFolder(accID, "INBOX", "inbox")
	archive, _ := db.EnsureFolder(accID, "Archive", "archive")
	// Subject sama, folder berbeda — fallback subject tidak boleh menyatukan.
	for _, f := range []int64{inbox, archive} {
		if _, err := db.UpsertMessage(&MessageHeader{
			FolderID: int(f), UID: 1, Subject: "Undangan rapat",
			Date: "2026-11-17T01:00:00Z",
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RegroupThreads(inbox); err != nil {
		t.Fatal(err)
	}
	if err := db.RegroupThreads(archive); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]int64{"inbox": inbox, "archive": archive} {
		threads, err := db.ListThreadSummaries(f, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(threads) != 1 || threads[0].ThreadCount != 1 {
			t.Fatalf("%s: thread %+v, mau 1 baris cnt 1", name, threads)
		}
	}
	var tInbox, tArchive string
	db.QueryRow(`SELECT thread_id FROM messages WHERE folder_id=?`, inbox).Scan(&tInbox)
	db.QueryRow(`SELECT thread_id FROM messages WHERE folder_id=?`, archive).Scan(&tArchive)
	if tInbox == tArchive {
		t.Fatalf("thread lintas folder menyatu: %q", tInbox)
	}
}

func TestListThreadSummariesSearchFlat(t *testing.T) {
	db := openTestDB(t)
	folder := testFolder(t, db)
	for i, subj := range []string{"laporan Q1", "Re: laporan Q1", "laporan Q2"} {
		var mid, irt string
		if i == 1 {
			irt = "<q1@x>"
		} else {
			mid = fmt.Sprintf("<q%d@x>", i+1)
		}
		if _, err := db.UpsertMessage(&MessageHeader{
			FolderID: int(folder), UID: i + 1, MessageID: mid, InReplyTo: irt,
			Subject: subj, Date: fmt.Sprintf("2026-11-17T0%d:00:00Z", i+1),
		}, &MessageBody{PlainText: subj}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RegroupThreads(folder); err != nil {
		t.Fatal(err)
	}
	// Tanpa query: 2 thread (Q1 gabung, Q2 sendiri).
	threads, err := db.ListThreadSummaries(folder, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("thread %+v, mau 2", threads)
	}
	// Dengan query FTS: tetap per pesan (flat), ThreadCount kosong.
	hits, err := db.ListThreadSummaries(folder, "laporan")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("pencarian %+v, mau 3 pesan", hits)
	}
	for _, h := range hits {
		if h.ThreadCount != 0 || h.IsThreadLatest {
			t.Fatalf("pencarian harus flat: %+v", h)
		}
	}
}

func TestSetMessageThreadHeaders(t *testing.T) {
	db := openTestDB(t)
	folder := testFolder(t, db)
	id, err := db.UpsertMessage(&MessageHeader{
		FolderID: int(folder), UID: 1, Subject: "Re: topik", Date: "2026-11-17T01:00:00Z",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Melengkapi kolom kosong dan memberi thread_id.
	if err := db.SetMessageThreadHeaders(id, "<b@x>", "<a@x>", "<a@x> <b@x>"); err != nil {
		t.Fatal(err)
	}
	var mid, irt, refs, thread string
	if err := db.QueryRow(`SELECT message_id, in_reply_to, refs, thread_id
		FROM messages WHERE id=?`, id).Scan(&mid, &irt, &refs, &thread); err != nil {
		t.Fatal(err)
	}
	if mid != "<b@x>" || irt != "<a@x>" || refs != "<a@x> <b@x>" {
		t.Fatalf("header %+v %+v %+v", mid, irt, refs)
	}
	if thread != "a@x" {
		t.Fatalf("thread_id=%q mau a@x", thread)
	}
	// Tidak menimpa data yang sudah ada (sync datang tanpa refs).
	if err := db.SetMessageThreadHeaders(id, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT message_id, in_reply_to, refs, thread_id
		FROM messages WHERE id=?`, id).Scan(&mid, &irt, &refs, &thread); err != nil {
		t.Fatal(err)
	}
	if mid != "<b@x>" || irt != "<a@x>" || refs != "<a@x> <b@x>" || thread != "a@x" {
		t.Fatalf("data tertimpa: %q %q %q %q", mid, irt, refs, thread)
	}
	// Pesan tidak ada → error.
	if err := db.SetMessageThreadHeaders(id+99, "<x>", "", ""); err == nil {
		t.Fatal("pesan tidak ada harus error")
	}
}

// TestMigrationBackfillThreads membangun database skema v3 (sebelum kolom
// thread_id), mengisinya dengan rantai reply, lalu membukanya lewat Open()
// sehingga migrasi v4 + backfill dijalankan. Semua pesan lama harus sudah
// tergrup tanpa sync ulang.
func TestMigrationBackfillThreads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lama.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Bangun skema v1..v3 manual, dan catat versinya ke schema_migrations
	// agar Open() tidak menjalankan ulang migrasi lama.
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER
		PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT
		(strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < threadSchemaVersion-1; i++ {
		if _, err := raw.Exec(migrations[i]); err != nil {
			t.Fatalf("migrasi v%d manual: %v", i+1, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, i+1); err != nil {
			t.Fatalf("catat migrasi v%d: %v", i+1, err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO accounts (email, imap_host, imap_port, imap_tls,
		smtp_host, smtp_port, smtp_tls) VALUES ('lama@contoh.id','imap',993,1,'smtp',465,1)`); err != nil {
		t.Fatal(err)
	}
	var accID, folderID int64
	if err := raw.QueryRow(`SELECT id FROM accounts`).Scan(&accID); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO folders (account_id, name, role) VALUES (?, 'INBOX','inbox')`, accID); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT id FROM folders`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	// Skema v3: TANPA kolom thread_id/refs (in_reply_to ada sejak v1).
	seed := []struct {
		uid                  int
		mid, irt, subj, date string
	}{
		{1, "<root@lama>", "", "Rencana rilis", "2026-11-17T01:00:00Z"},
		{2, "<r1@lama>", "<root@lama>", "Re: Rencana rilis", "2026-11-17T02:00:00Z"},
		{3, "", "", "Catatan pribadi", "2026-11-17T00:00:00Z"},
	}
	for _, s := range seed {
		if _, err := raw.Exec(`INSERT INTO messages (folder_id, uid, message_id,
			in_reply_to, subject, date) VALUES (?,?,?,?,?,?)`,
			folderID, s.uid, s.mid, s.irt, s.subj, s.date); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	// Buka lewat Open(): migrasi v4 berjalan + backfill thread lama.
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < threadSchemaVersion {
		t.Fatalf("versi skema %d, mau >= %d", version, threadSchemaVersion)
	}
	threads, err := db.ListThreadSummaries(folderID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("thread setelah backfill %+v, mau 2", threads)
	}
	// Baris teratas = pesan terbaru (r1) dengan thread berisi 2 anggota.
	if threads[0].ThreadCount != 2 || threads[0].Subject != "Re: Rencana rilis" {
		t.Fatalf("backfill salah kelompok: %+v", threads[0])
	}
	if threads[1].ThreadCount != 1 || threads[1].Subject != "Catatan pribadi" {
		t.Fatalf("backfill salah kelompok: %+v", threads[1])
	}
	var tRoot, tReply string
	if err := db.QueryRow(`SELECT thread_id FROM messages WHERE message_id='<root@lama>'`).Scan(&tRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT thread_id FROM messages WHERE in_reply_to='<root@lama>'`).Scan(&tReply); err != nil {
		t.Fatal(err)
	}
	if tRoot == "" || tRoot != tReply {
		t.Fatalf("thread backfill tidak menyatu: %q vs %q", tRoot, tReply)
	}
}
