// Package app adalah application service layer: facade tunggal antara UI,
// protokol email, dan penyimpanan. Semua struct dan method harus JSON-friendly
// agar dapat diekspos lewat internal/bridge ke Takka (Rust) di masa depan.
package app

// Account adalah akun email yang dikonfigurasi.
type Account struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

// AuthMethod menentukan cara login ke server email.
type AuthMethod string

const (
	AuthPassword AuthMethod = "password" // default; App Password untuk Gmail
	AuthOAuth2   AuthMethod = "oauth2"   // XOAUTH2 (Google)
)

// Folder adalah folder IMAP pada satu akun.
type Folder struct {
	ID        int    `json:"id"`
	AccountID int    `json:"accountId"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Unread    int    `json:"unread"`
}

// MessageHeader adalah metadata ringan satu pesan untuk daftar pesan.
type MessageHeader struct {
	ID            int    `json:"id"`
	FolderID      int    `json:"folderId"`
	UID           int    `json:"uid"`
	Subject       string `json:"subject"`
	Sender        string `json:"sender"`
	SenderAddr    string `json:"senderAddr"`
	Date          string `json:"date"`
	Unread        bool   `json:"unread"`
	HasAttachment bool   `json:"hasAttachment"`
}

// Attachment adalah lampiran satu pesan.
type Attachment struct {
	ID          int    `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// Message adalah satu pesan lengkap untuk reading pane.
type Message struct {
	MessageHeader
	To          string       `json:"to"`
	Cc          string       `json:"cc"`
	PlainText   string       `json:"plainText"`
	RawHTML     string       `json:"rawHtml"`
	Fetched     bool         `json:"fetched"`
	Attachments []Attachment `json:"attachments"`
}

// AccountConfig adalah data konfigurasi akun baru. Password/token dikirim
// ke keyring dan tidak pernah disimpan di database maupun file.
type AccountConfig struct {
	Email    string     `json:"email"`
	Password string     `json:"-"` // tidak pernah ikut serialisasi
	IMAPHost string     `json:"imapHost"`
	IMAPPort int        `json:"imapPort"`
	IMAPTLS  bool       `json:"imapTls"`
	SMTPHost string     `json:"smtpHost"`
	SMTPPort int        `json:"smtpPort"`
	SMTPTLS  bool       `json:"smtpTls"`
	Auth     AuthMethod `json:"auth"` // kosong = password
}

// Draft adalah pesan yang sedang disusun: Compose baru, Reply, atau
// Forward. Attachments berisi path file lokal yang dilampirkan.
type Draft struct {
	To          string   `json:"to"`
	Cc          string   `json:"cc"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"`
}
