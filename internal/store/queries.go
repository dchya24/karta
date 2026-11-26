package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Account adalah baris tabel accounts. Secret TIDAK ada di sini (keyring).
type Account struct {
	ID       int
	Email    string
	IMAPHost string
	IMAPPort int
	IMAPTLS  bool
	SMTPHost string
	SMTPPort int
	SMTPTLS  bool
	Auth     string // "password" (default) | "oauth2"
}

// Folder adalah folder IMAP terdaftar beserta hitungan belum dibaca.
type Folder struct {
	ID          int
	AccountID   int
	Name        string
	Role        string
	Unread      int
	LastSeenUID uint32
}

// MessageHeader adalah metadata ringan satu pesan untuk daftar pesan.
// Header reply (InReplyTo/Refs) dan ThreadID dipakai pengelompokan
// percakapan; ThreadCount/IsThreadLatest hanya terisi oleh
// ListThreadSummaries (satu baris per thread).
type MessageHeader struct {
	ID             int
	FolderID       int
	UID            int
	MessageID      string
	InReplyTo      string
	Refs           string // header References mentah, dipisah spasi
	Subject        string
	Sender         string
	SenderAddr     string
	Recipients     string
	Cc             string
	Date           string
	Unread         bool
	HasAttachment  bool
	Size           int
	ThreadID       string
	ThreadCount    int
	IsThreadLatest bool
}

// headerCols adalah daftar kolom header pesan untuk SELECT, dengan alias
// agregasi thread (thread_count/is_thread_latest) bernilai 0 pada query
// per-pesan biasa. Urutannya mengikuti scanHeaders.
const headerCols = `id, folder_id, uid, COALESCE(message_id,''),
		COALESCE(in_reply_to,''), COALESCE(refs,''), COALESCE(subject,''),
		COALESCE(sender,''), COALESCE(sender_addr,''), COALESCE(recipients,''),
		COALESCE(cc,''), COALESCE(date,''), unread, has_attachment, size,
		COALESCE(thread_id,''), 0 AS thread_count, 0 AS is_thread_latest`

// MessageBody melengkapi header dengan isi pesan.
type MessageBody struct {
	PlainText string
	RawHTML   string
	Fetched   bool
}

// AddAccount menyimpan akun baru; kembalikan ID-nya.
func (db *DB) AddAccount(email, imapHost string, imapPort int, imapTLS bool, smtpHost string, smtpPort int, smtpTLS bool, auth string) (int64, error) {
	if auth == "" {
		auth = "password"
	}
	res, err := db.Exec(`INSERT INTO accounts
		(email, imap_host, imap_port, imap_tls, smtp_host, smtp_port, smtp_tls, auth)
		VALUES (?,?,?,?,?,?,?,?)`,
		email, imapHost, imapPort, boolInt(imapTLS), smtpHost, smtpPort, boolInt(smtpTLS), auth)
	if err != nil {
		return 0, fmt.Errorf("simpan akun %s: %w", email, err)
	}
	return res.LastInsertId()
}

// UpdateAccount memperbarui konfigurasi koneksi akun berdasar email.
func (db *DB) UpdateAccount(email, imapHost string, imapPort int, imapTLS bool, smtpHost string, smtpPort int, smtpTLS bool, auth string) error {
	if auth == "" {
		auth = "password"
	}
	res, err := db.Exec(`UPDATE accounts SET imap_host=?, imap_port=?, imap_tls=?,
		smtp_host=?, smtp_port=?, smtp_tls=?, auth=? WHERE email=?`,
		imapHost, imapPort, boolInt(imapTLS), smtpHost, smtpPort, boolInt(smtpTLS), auth, email)
	if err != nil {
		return fmt.Errorf("perbarui akun %s: %w", email, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("perbarui akun %s: tidak ditemukan", email)
	}
	return nil
}

// ListAccounts mengembalikan semua akun terurut email.
func (db *DB) ListAccounts() ([]Account, error) {
	rows, err := db.Query(`SELECT id, email, imap_host, imap_port, imap_tls,
		smtp_host, smtp_port, smtp_tls, auth FROM accounts ORDER BY email`)
	if err != nil {
		return nil, fmt.Errorf("daftar akun: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var itls, stls int
		if err := rows.Scan(&a.ID, &a.Email, &a.IMAPHost, &a.IMAPPort, &itls,
			&a.SMTPHost, &a.SMTPPort, &stls, &a.Auth); err != nil {
			return nil, fmt.Errorf("baca akun: %w", err)
		}
		a.IMAPTLS, a.SMTPTLS = itls == 1, stls == 1
		if a.Auth == "" {
			a.Auth = "password"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EnsureFolder memastikan folder terdaftar dan mengembalikan ID-nya.
func (db *DB) EnsureFolder(accountID int64, name, role string) (int64, error) {
	_, err := db.Exec(`INSERT INTO folders (account_id, name, role)
		VALUES (?,?,?)
		ON CONFLICT(account_id, name) DO UPDATE SET role=excluded.role`,
		accountID, name, role)
	if err != nil {
		return 0, fmt.Errorf("daftarkan folder %s: %w", name, err)
	}
	// Jangan percaya LastInsertId modernc/sqlite setelah ON CONFLICT DO
	// UPDATE (rowid fiktif); ambil ulang id lewat unique key.
	var id int64
	err = db.QueryRow(`SELECT id FROM folders WHERE account_id=? AND name=?`,
		accountID, name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("cari folder %s: %w", name, err)
	}
	return id, nil
}

// ListFolders mengembalikan folder milik akun beserta hitungan belum dibaca.
func (db *DB) ListFolders(accountID int64) ([]Folder, error) {
	rows, err := db.Query(`SELECT f.id, f.account_id, f.name, f.role, f.last_seen_uid,
			(SELECT COUNT(*) FROM messages m WHERE m.folder_id=f.id AND m.unread=1)
		FROM folders f WHERE f.account_id=? ORDER BY f.id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("daftar folder: %w", err)
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.LastSeenUID, &f.Unread); err != nil {
			return nil, fmt.Errorf("baca folder: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateFolderSync menyimpan posisi sinkronisasi folder (UID tertinggi).
func (db *DB) UpdateFolderSync(folderID int64, lastUID uint32) error {
	if _, err := db.Exec(`UPDATE folders SET last_seen_uid=? WHERE id=?`, lastUID, folderID); err != nil {
		return fmt.Errorf("update posisi sync folder %d: %w", folderID, err)
	}
	return nil
}

// FolderByID mengembalikan satu folder (untuk menelusuri akun dan nama IMAP).
func (db *DB) FolderByID(id int64) (*Folder, error) {
	f := &Folder{}
	err := db.QueryRow(`SELECT id, account_id, name, role, last_seen_uid,
			(SELECT COUNT(*) FROM messages m WHERE m.folder_id=folders.id AND m.unread=1)
		FROM folders WHERE id=?`, id).
		Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.LastSeenUID, &f.Unread)
	if err != nil {
		return nil, fmt.Errorf("baca folder %d: %w", id, err)
	}
	return f, nil
}

// FolderByRole mencari folder pertama akun dengan peran tertentu (archive, trash, dll.).
func (db *DB) FolderByRole(accountID int64, role string) (*Folder, error) {
	f := &Folder{}
	err := db.QueryRow(`SELECT id, account_id, name, role, last_seen_uid,
			(SELECT COUNT(*) FROM messages m WHERE m.folder_id=folders.id AND m.unread=1)
		FROM folders WHERE account_id=? AND role=? ORDER BY id LIMIT 1`, accountID, role).
		Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.LastSeenUID, &f.Unread)
	if err != nil {
		return nil, fmt.Errorf("folder peran %q akun %d: %w", role, accountID, err)
	}
	return f, nil
}

// ListAttachments mengembalikan lampiran satu pesan (tanpa isi blob).
func (db *DB) ListAttachments(messageID int64) ([][2]string, error) {
	rows, err := db.Query(`SELECT filename, COALESCE(content_type,'') FROM attachments
		WHERE message_id=? ORDER BY id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("daftar lampiran pesan %d: %w", messageID, err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var fn, ct string
		if err := rows.Scan(&fn, &ct); err != nil {
			return nil, fmt.Errorf("baca lampiran: %w", err)
		}
		out = append(out, [2]string{fn, ct})
	}
	return out, rows.Err()
}

// InsertAttachment menyimpan satu lampiran pesan.
func (db *DB) InsertAttachment(messageID int64, filename, contentType string, data []byte) error {
	if _, err := db.Exec(`INSERT INTO attachments (message_id, filename, content_type, size, data)
		VALUES (?,?,?,?,?)`, messageID, filename, contentType, len(data), data); err != nil {
		return fmt.Errorf("simpan lampiran %q: %w", filename, err)
	}
	return nil
}

// UpsertMessage menyimpan header pesan (dan isi bila sudah di-fetch),
// mengindeks teksnya ke FTS5, dan mengembalikan ID barisnya. thread_id
// dihitung saat insert (ThreadSeedKey); RegroupThreads yang menyatukan
// rantai lintas pesan.
func (db *DB) UpsertMessage(m *MessageHeader, body *MessageBody) (int64, error) {
	var fetched int
	var plain, raw sql.NullString
	if body != nil {
		fetched = 1
		plain, raw = sql.NullString{String: body.PlainText, Valid: true},
			sql.NullString{String: body.RawHTML, Valid: true}
	}
	threadID := m.ThreadID
	if threadID == "" {
		threadID = ThreadSeedKey(int64(m.FolderID), m.MessageID, m.InReplyTo, m.Refs, m.Subject)
	}
	if threadID == "" {
		// Tanpa identitas apa pun: singleton unik agar tidak ikut tergrup
		// dengan pesan kosong lain (RegroupThreads membuat kunci serupa).
		threadID = fmt.Sprintf("u%d:%d", m.FolderID, m.UID)
	}
	res, err := db.Exec(`INSERT INTO messages
		(folder_id, uid, message_id, in_reply_to, refs, subject, sender, sender_addr,
		 recipients, cc, date, unread, has_attachment, size, thread_id,
		 plain_text, raw_html, fetched)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			message_id=excluded.message_id, subject=excluded.subject,
			sender=excluded.sender, sender_addr=excluded.sender_addr,
			recipients=excluded.recipients, cc=excluded.cc, date=excluded.date,
			has_attachment=excluded.has_attachment, size=excluded.size,
			in_reply_to=CASE WHEN excluded.in_reply_to!='' THEN excluded.in_reply_to ELSE messages.in_reply_to END,
			refs=CASE WHEN excluded.refs!='' THEN excluded.refs ELSE messages.refs END,
			thread_id=CASE WHEN messages.thread_id='' THEN excluded.thread_id ELSE messages.thread_id END`,
		m.FolderID, m.UID, m.MessageID, m.InReplyTo, m.Refs, m.Subject, m.Sender,
		m.SenderAddr, m.Recipients, m.Cc, m.Date, boolInt(m.Unread),
		boolInt(m.HasAttachment), m.Size, threadID, plain, raw, fetched)
	if err != nil {
		return 0, fmt.Errorf("simpan pesan uid=%d folder=%d: %w", m.UID, m.FolderID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		if err := db.QueryRow(`SELECT id FROM messages WHERE folder_id=? AND uid=?`,
			m.FolderID, m.UID).Scan(&id); err != nil {
			return 0, fmt.Errorf("cari pesan uid=%d: %w", m.UID, err)
		}
	}
	if body != nil {
		if err := db.indexMessage(id, m, body); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// DeleteAccount menghapus akun beserta salinan indeks FTS yang ditinggalkan
// cascade delete dalam satu transaksi.
func (db *DB) DeleteAccount(id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai transaksi hapus akun %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM accounts WHERE id=?`, id); err != nil {
		return fmt.Errorf("hapus akun %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM messages_fts WHERE rowid NOT IN (SELECT id FROM messages)`); err != nil {
		return fmt.Errorf("bersihkan indeks FTS: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hapus akun %d: %w", id, err)
	}
	return nil
}

// indexMessage menyegarkan baris FTS5 untuk satu pesan (FTS5 tidak
// mendukung UPSERT: hapus lalu sisip).
func (db *DB) indexMessage(id int64, m *MessageHeader, body *MessageBody) error {
	if _, err := db.Exec(`DELETE FROM messages_fts WHERE rowid=?`, id); err != nil {
		return fmt.Errorf("hapus indeks FTS pesan %d: %w", id, err)
	}
	recips := strings.TrimSpace(m.Recipients + " " + m.Cc)
	if _, err := db.Exec(`INSERT INTO messages_fts(rowid, subject, sender_addr, recipients, plain_text)
		VALUES (?,?,?,?,?)`,
		id, m.Subject, m.SenderAddr, recips, body.PlainText); err != nil {
		return fmt.Errorf("indeks FTS pesan %d: %w", id, err)
	}
	return nil
}

// SetMessageBody menyimpan isi pesan yang baru di-fetch lazy.
func (db *DB) SetMessageBody(id int64, body *MessageBody) error {
	if _, err := db.Exec(`UPDATE messages SET plain_text=?, raw_html=?, fetched=1 WHERE id=?`,
		body.PlainText, body.RawHTML, id); err != nil {
		return fmt.Errorf("simpan body pesan %d: %w", id, err)
	}
	var m MessageHeader
	if err := db.QueryRow(`SELECT folder_id, uid, COALESCE(message_id,''),
			COALESCE(subject,''), COALESCE(sender,''), COALESCE(sender_addr,''),
			COALESCE(recipients,''), COALESCE(cc,'')
		FROM messages WHERE id=?`, id).
		Scan(&m.FolderID, &m.UID, &m.MessageID, &m.Subject, &m.Sender,
			&m.SenderAddr, &m.Recipients, &m.Cc); err != nil {
		return fmt.Errorf("baca header pesan %d: %w", id, err)
	}
	return db.indexMessage(id, &m, body)
}

// MoveMessage memindahkan pesan ke folder tujuan secara atomik; menghindari
// bentrok UNIQUE(folder_id, uid) dengan UID sementara negatif bila perlu.
func (db *DB) MoveMessage(id, destFolderID int64, newUID uint32) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai transaksi pindah pesan %d: %w", id, err)
	}
	defer tx.Rollback()
	var uid int64
	if newUID > 0 {
		uid = int64(newUID)
	} else {
		if err := tx.QueryRow(`SELECT uid FROM messages WHERE id=?`, id).Scan(&uid); err != nil {
			return fmt.Errorf("baca uid pesan %d: %w", id, err)
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE folder_id=? AND uid=? AND id!=?`,
			destFolderID, uid, id).Scan(&exists); err != nil {
			return fmt.Errorf("cek uid tujuan: %w", err)
		}
		if exists > 0 {
			uid = -id
		}
	}
	if _, err := tx.Exec(`UPDATE messages SET folder_id=?, uid=? WHERE id=?`,
		destFolderID, uid, id); err != nil {
		return fmt.Errorf("pindah pesan %d ke folder %d: %w", id, destFolderID, err)
	}
	return tx.Commit()
}

// SetMessageUID memperbarui UID lokal setelah IMAP MOVE mengembalikan UID baru.
func (db *DB) SetMessageUID(id int64, uid uint32) error {
	if _, err := db.Exec(`UPDATE messages SET uid=? WHERE id=?`, uid, id); err != nil {
		return fmt.Errorf("update uid pesan %d: %w", id, err)
	}
	return nil
}

// DeleteMessage menghapus pesan beserta baris FTS-nya dalam satu transaksi.
func (db *DB) DeleteMessage(id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai transaksi hapus pesan %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE id=?`, id); err != nil {
		return fmt.Errorf("hapus pesan %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM messages_fts WHERE rowid=?`, id); err != nil {
		return fmt.Errorf("hapus FTS pesan %d: %w", id, err)
	}
	return tx.Commit()
}

// ListMessages mengembalikan pesan pada folder, terbaru dulu. Bila query
// tidak kosong, hasil difilter lewat FTS5.
func (db *DB) ListMessages(folderID int64, query string) ([]MessageHeader, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if query == "" {
		rows, err = db.Query(`SELECT `+headerCols+` FROM messages
			WHERE folder_id=? ORDER BY date DESC, uid DESC`, folderID)
	} else {
		// FTS-driven: ambil rowid hasil MATCH dulu, lalu ambil baris per
		// batch via PK. JOIN lewat IN-subquery membuat planner memindai
		// seluruh folder (10k+ baris) sebelum memfilter.
		ids, err := db.selectFTSRowIDs(query)
		if err != nil {
			return nil, err
		}
		out := make([]MessageHeader, 0, len(ids))
		for start := 0; start < len(ids); start += 500 {
			end := min(start+500, len(ids))
			rows, err := db.fetchByIDs(folderID, ids[start:end])
			if err != nil {
				return nil, err
			}
			hdrs, err := scanHeaders(rows)
			rows.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, hdrs...)
		}
		sortMessageHeaders(out)
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("daftar pesan folder %d: %w", folderID, err)
	}
	defer rows.Close()
	return scanHeaders(rows)
}

// ListThreadSummaries mengembalikan satu baris per thread folder: pesan
// terbaru tiap thread (IsThreadLatest=true) beserta jumlah anggota thread
// (ThreadCount), terurut pesan terbaru dulu. Query pencarian (FTS5) tetap
// menampilkan pesan per baris tanpa agregasi thread.
func (db *DB) ListThreadSummaries(folderID int64, query string) ([]MessageHeader, error) {
	if query != "" {
		return db.ListMessages(folderID, query)
	}
	// Pesan terbaru tiap thread via subquery terindeks (folder, thread, date,
	// uid) + hitungan anggota lewat GROUP BY pada index yang sama; tanpa
	// memindai tabel di luar folder. Kolom thread_id di-qualify karena join
	// membuatnya ambigu.
	mCols := strings.ReplaceAll(headerCols, "COALESCE(thread_id,'')",
		"COALESCE(m.thread_id,'')")
	rows, err := db.Query(`SELECT `+mCols+`, t.cnt, 1
		FROM messages m
		JOIN (
			SELECT thread_id, COUNT(*) AS cnt
			FROM messages WHERE folder_id=? GROUP BY thread_id
		) t ON t.thread_id = m.thread_id
		WHERE m.folder_id=? AND m.id = (
			SELECT m2.id FROM messages m2
			WHERE m2.folder_id = m.folder_id AND m2.thread_id = m.thread_id
			ORDER BY m2.date DESC, m2.uid DESC LIMIT 1
		)
		ORDER BY m.date DESC, m.uid DESC`, folderID, folderID)
	if err != nil {
		return nil, fmt.Errorf("daftar thread folder %d: %w", folderID, err)
	}
	defer rows.Close()
	var out []MessageHeader
	for rows.Next() {
		h := MessageHeader{}
		// Dua kolom agregasi ekstra (t.cnt, 1) menimpa hasil headerCols.
		var cnt, latest int
		if err := scanHeaderRow(func(dst ...any) error {
			args := make([]any, 0, len(dst)+2)
			args = append(args, dst...)
			args = append(args, &cnt, &latest)
			return rows.Scan(args...)
		}, &h); err != nil {
			return nil, fmt.Errorf("baca thread: %w", err)
		}
		h.ThreadCount, h.IsThreadLatest = cnt, latest == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetMessageThreadHeaders melengkapi header reply pesan dari hasil parsing
// raw (dipanggil saat body di-fetch lazy). Hanya kolom yang masih kosong
// yang diisi agar data dari sync tidak tertimpa. Setelah header terisi,
// thread folder dihitung ulang (regroup) agar rantai reply tersatukan.
func (db *DB) SetMessageThreadHeaders(id int64, messageID, inReplyTo, refs string) error {
	var folderID int64
	var curMid, curIrt, curRefs, curSubj, curThread string
	err := db.QueryRow(`SELECT folder_id, COALESCE(message_id,''),
			COALESCE(in_reply_to,''), COALESCE(refs,''), COALESCE(subject,''),
			COALESCE(thread_id,'')
		FROM messages WHERE id=?`, id).
		Scan(&folderID, &curMid, &curIrt, &curRefs, &curSubj, &curThread)
	if err != nil {
		return fmt.Errorf("baca header pesan %d: %w", id, err)
	}
	if curMid == "" {
		curMid = strings.TrimSpace(messageID)
	}
	if curIrt == "" {
		curIrt = strings.TrimSpace(inReplyTo)
	}
	if curRefs == "" {
		curRefs = strings.Join(strings.Fields(refs), " ")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai tx header pesan %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE messages SET message_id=?, in_reply_to=?, refs=?
		WHERE id=?`, curMid, curIrt, curRefs, id); err != nil {
		return fmt.Errorf("lengkapi header thread pesan %d: %w", id, err)
	}
	// thread_id dihitung ulang seluruh folder: header baru bisa menyatukan
	// atau memecah thread (regroup deterministik & idempoten).
	if err := regroupThreadsTx(tx, folderID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit header pesan %d: %w", id, err)
	}
	return nil
}

// selectFTSRowIDs mengembalikan rowid pesan yang cocok query FTS.
func (db *DB) selectFTSRowIDs(query string) ([]int64, error) {
	rows, err := db.Query(`SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?`,
		ftsQuery(query))
	if err != nil {
		return nil, fmt.Errorf("cari FTS: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("baca hasil FTS: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// fetchByIDs mengambil header pesan berdasarkan ID (batch, PK lookup).
func (db *DB) fetchByIDs(folderID int64, ids []int64) (*sql.Rows, error) {
	ph := strings.Repeat("?,", len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, folderID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(`SELECT `+headerCols+` FROM messages
		WHERE folder_id=? AND id IN (`+ph[:len(ph)-1]+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("ambil pesan batch: %w", err)
	}
	return rows, nil
}

// sortMessageHeaders mengurutkan terbaru dulu (date DESC, lalu UID DESC).
func sortMessageHeaders(m []MessageHeader) {
	sort.Slice(m, func(i, j int) bool {
		if m[i].Date != m[j].Date {
			return m[i].Date > m[j].Date
		}
		return m[i].UID > m[j].UID
	})
}

// GetMessage mengembalikan header dan isi satu pesan.
func (db *DB) GetMessage(id int64) (*MessageHeader, *MessageBody, error) {
	h, err := db.queryHeader(`SELECT `+headerCols+` FROM messages WHERE id=?`, id)
	if err != nil {
		return nil, nil, err
	}
	var b MessageBody
	err = db.QueryRow(`SELECT COALESCE(plain_text,''), COALESCE(raw_html,''), fetched
		FROM messages WHERE id=?`, id).Scan(&b.PlainText, &b.RawHTML, &b.Fetched)
	if err != nil {
		return nil, nil, fmt.Errorf("baca body pesan %d: %w", id, err)
	}
	return h, &b, nil
}

// MarkRead mengubah status baca satu pesan.
func (db *DB) MarkRead(id int64, read bool) error {
	if _, err := db.Exec(`UPDATE messages SET unread=? WHERE id=?`,
		boolInt(!read), id); err != nil {
		return fmt.Errorf("tandai baca pesan %d: %w", id, err)
	}
	return nil
}

// scanHeaderRow memindai satu baris header sesuai urutan headerCols plus
// dua kolom agregasi thread (thread_count, is_thread_latest).
func scanHeaderRow(scan func(...any) error, h *MessageHeader) error {
	var unread, att, cnt, latest int
	if err := scan(&h.ID, &h.FolderID, &h.UID, &h.MessageID, &h.InReplyTo, &h.Refs,
		&h.Subject, &h.Sender, &h.SenderAddr, &h.Recipients, &h.Cc, &h.Date,
		&unread, &att, &h.Size, &h.ThreadID, &cnt, &latest); err != nil {
		return err
	}
	h.Unread, h.HasAttachment = unread == 1, att == 1
	h.ThreadCount, h.IsThreadLatest = cnt, latest == 1
	return nil
}

// queryHeader menjalankan query satu baris header.
func (db *DB) queryHeader(q string, args ...any) (*MessageHeader, error) {
	h := &MessageHeader{}
	if err := scanHeaderRow(func(dst ...any) error {
		return db.QueryRow(q, args...).Scan(dst...)
	}, h); err != nil {
		return nil, fmt.Errorf("baca header pesan: %w", err)
	}
	return h, nil
}

func scanHeaders(rows *sql.Rows) ([]MessageHeader, error) {
	var out []MessageHeader
	for rows.Next() {
		h := MessageHeader{}
		if err := scanHeaderRow(rows.Scan, &h); err != nil {
			return nil, fmt.Errorf("baca pesan: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery membungkus query pengguna agar diperlakukan sebagai kumpulan
// token (prefix match), bukan sintaks FTS mentah.
func ftsQuery(q string) string {
	out := ""
	for i, tok := range splitTokens(q) {
		if i > 0 {
			out += " "
		}
		out += `"` + tok + `"*`
	}
	return out
}

func splitTokens(q string) []string {
	var toks []string
	cur := ""
	for _, r := range q {
		switch {
		case r == ' ' || r == '\t':
			if cur != "" {
				toks = append(toks, cur)
				cur = ""
			}
		case r == '"':
			// lewati tanda kutip pengguna
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		toks = append(toks, cur)
	}
	return toks
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
