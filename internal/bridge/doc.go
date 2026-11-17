// Package bridge adalah stub IPC local socket untuk integrasi Takka (Rust)
// di masa depan. v0.1 tidak mengaktifkan fitur apa pun di sini; boundary
// abstraksinya adalah internal/app.Service (DTO JSON-friendly).
package bridge

// Start saat ini tidak melakukan apa-apa dan selalu sukses. Implementasi
// socket lokal akan menggantikannya tanpa mengubah pemanggil.
func Start() error { return nil }
