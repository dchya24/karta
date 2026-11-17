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
type MessageHeader struct {
	ID            int
	FolderID      int
	UID           int
	MessageID     string
	Subject       string
	Sender        string
	SenderAddr    string
	Recipients    string
	Date          string
	Unread        bool
	HasAttachment bool
	Size          int
}

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
	res, err := db.Exec(`INSERT INTO folders (account_id, name, role)
		VALUES (?,?,?)
		ON CONFLICT(account_id, name) DO UPDATE SET role=excluded.role`,
		accountID, name, role)
	if err != nil {
		return 0, fmt.Errorf("daftarkan folder %s: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		// Konflik: ambil ID yang sudah ada.
		err = db.QueryRow(`SELECT id FROM folders WHERE account_id=? AND name=?`,
			accountID, name).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("cari folder %s: %w", name, err)
		}
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
// mengindeks teksnya ke FTS5, dan mengembalikan ID barisnya.
func (db *DB) UpsertMessage(m *MessageHeader, body *MessageBody) (int64, error) {
	var fetched int
	var plain, raw sql.NullString
	if body != nil {
		fetched = 1
		plain, raw = sql.NullString{String: body.PlainText, Valid: true},
			sql.NullString{String: body.RawHTML, Valid: true}
	}
	res, err := db.Exec(`INSERT INTO messages
		(folder_id, uid, message_id, subject, sender, sender_addr, recipients,
		 date, unread, has_attachment, size, plain_text, raw_html, fetched)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			message_id=excluded.message_id, subject=excluded.subject,
			sender=excluded.sender, sender_addr=excluded.sender_addr,
			recipients=excluded.recipients, date=excluded.date,
			has_attachment=excluded.has_attachment, size=excluded.size`,
		m.FolderID, m.UID, m.MessageID, m.Subject, m.Sender, m.SenderAddr,
		m.Recipients, m.Date, boolInt(m.Unread), boolInt(m.HasAttachment),
		m.Size, plain, raw, fetched)
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

// indexMessage menyegarkan baris FTS5 untuk satu pesan (FTS5 tidak
// mendukung UPSERT: hapus lalu sisip).
func (db *DB) indexMessage(id int64, m *MessageHeader, body *MessageBody) error {
	if _, err := db.Exec(`DELETE FROM messages_fts WHERE rowid=?`, id); err != nil {
		return fmt.Errorf("hapus indeks FTS pesan %d: %w", id, err)
	}
	if _, err := db.Exec(`INSERT INTO messages_fts(rowid, subject, sender_addr, recipients, plain_text)
		VALUES (?,?,?,?,?)`,
		id, m.Subject, m.SenderAddr, m.Recipients, body.PlainText); err != nil {
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
			COALESCE(recipients,'')
		FROM messages WHERE id=?`, id).
		Scan(&m.FolderID, &m.UID, &m.MessageID, &m.Subject, &m.Sender,
			&m.SenderAddr, &m.Recipients); err != nil {
		return fmt.Errorf("baca header pesan %d: %w", id, err)
	}
	return db.indexMessage(id, &m, body)
}

// ListMessages mengembalikan pesan pada folder, terbaru dulu. Bila query
// tidak kosong, hasil difilter lewat FTS5.
func (db *DB) ListMessages(folderID int64, query string) ([]MessageHeader, error) {
	const cols = `id, folder_id, uid, COALESCE(message_id,''), COALESCE(subject,''),
		COALESCE(sender,''), COALESCE(sender_addr,''), COALESCE(recipients,''),
		COALESCE(date,''), unread, has_attachment, size`
	var (
		rows *sql.Rows
		err  error
	)
	if query == "" {
		rows, err = db.Query(`SELECT `+cols+` FROM messages
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
	const cols = `id, folder_id, uid, COALESCE(message_id,''), COALESCE(subject,''),
		COALESCE(sender,''), COALESCE(sender_addr,''), COALESCE(recipients,''),
		COALESCE(date,''), unread, has_attachment, size`
	rows, err := db.Query(`SELECT `+cols+` FROM messages
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
	h, err := db.queryHeader(`SELECT id, folder_id, uid, COALESCE(message_id,''),
			COALESCE(subject,''), COALESCE(sender,''), COALESCE(sender_addr,''),
			COALESCE(recipients,''), COALESCE(date,''), unread, has_attachment, size
		FROM messages WHERE id=?`, id)
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

// queryHeader menjalankan query satu baris header.
func (db *DB) queryHeader(q string, args ...any) (*MessageHeader, error) {
	row := db.QueryRow(q, args...)
	h := &MessageHeader{}
	var unread, att int
	if err := row.Scan(&h.ID, &h.FolderID, &h.UID, &h.MessageID, &h.Subject,
		&h.Sender, &h.SenderAddr, &h.Recipients, &h.Date, &unread, &att, &h.Size); err != nil {
		return nil, fmt.Errorf("baca header pesan: %w", err)
	}
	h.Unread, h.HasAttachment = unread == 1, att == 1
	return h, nil
}

func scanHeaders(rows *sql.Rows) ([]MessageHeader, error) {
	var out []MessageHeader
	for rows.Next() {
		h := MessageHeader{}
		var unread, att int
		if err := rows.Scan(&h.ID, &h.FolderID, &h.UID, &h.MessageID, &h.Subject,
			&h.Sender, &h.SenderAddr, &h.Recipients, &h.Date, &unread, &att, &h.Size); err != nil {
			return nil, fmt.Errorf("baca pesan: %w", err)
		}
		h.Unread, h.HasAttachment = unread == 1, att == 1
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
