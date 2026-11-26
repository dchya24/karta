package store

// Identitas thread percakapan (BRIEF thread-grouping):
//   - Sumber utama: rantai References + In-Reply-To → Message-ID dinormalisasi
//     (trim <>, lowercase/case-fold).
//   - Fallback: subject dinormalisasi (strip Re:/Fwd:/Fw:, spasi berlebih)
//     dan hanya untuk pesan tanpa header reply sama sekali, terikat folder.
// Thread selalu dikelompokkan per-folder (query memfilter folder_id).

import (
	"database/sql"
	"fmt"
	"strings"
)

// NormalizeMessageID menormalkan satu Message-ID: buang spasi dan kurung
// sudut, lalu case-fold ke lowercase. Aman untuk input kosong.
func NormalizeMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return strings.ToLower(strings.TrimSpace(id))
}

// splitMessageIDs memecah nilai header References/In-Reply-To menjadi daftar
// Message-ID yang sudah dinormalisasi (satu header bisa memuat banyak ID).
func splitMessageIDs(refs string) []string {
	fields := strings.Fields(refs)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if n := NormalizeMessageID(f); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// NormalizeThreadSubject menormalkan subject untuk fallback identitas thread:
// strip prefix reply/forward berulang (Re:, Fwd:, Fw:, termasuk Re[2]:) secara
// case-insensitive, lipat spasi berlebih, lalu case-fold. Prefix hanya
// dianggap reply bila diikuti ':' — "restart: x" tidak ikut di-strip.
func NormalizeThreadSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		low := strings.ToLower(s)
		var consumed int
		switch {
		case strings.HasPrefix(low, "fwd:"):
			consumed = len("fwd:")
		case strings.HasPrefix(low, "fwd["):
			consumed = stripCounterSuffix(low, len("fwd["))
		case strings.HasPrefix(low, "fw:"):
			consumed = len("fw:")
		case strings.HasPrefix(low, "fw["):
			consumed = stripCounterSuffix(low, len("fw["))
		case strings.HasPrefix(low, "re:"):
			consumed = len("re:")
		case strings.HasPrefix(low, "re["):
			consumed = stripCounterSuffix(low, len("re["))
		default:
			return foldSubject(s)
		}
		// consumed==0 berarti ada "[n]" tapi tidak diikuti ':' — berhenti.
		if consumed == 0 {
			return foldSubject(s)
		}
		s = strings.TrimSpace(s[consumed:])
	}
}

// stripCounterSuffix memeriksa pola "[digits]:" setelah prefix "re["/"fw["/
// "fwd[" (pos = indeks karakter setelah '['). Mengembalikan panjang prefix
// yang dikonsumsi sampai setelah ':', atau 0 bila pola tidak cocok.
func stripCounterSuffix(low string, pos int) int {
	j := pos
	for j < len(low) && low[j] >= '0' && low[j] <= '9' {
		j++
	}
	if j == pos || j >= len(low) || low[j] != ']' {
		return 0
	}
	j++
	if j >= len(low) || low[j] != ':' {
		return 0
	}
	return j + 1
}

// foldSubject melipat run spasi menjadi satu spasi dan case-fold.
func foldSubject(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// ThreadSeedKey menghasilkan kunci thread awal untuk pesan baru saat insert
// (sebelum RegroupThreads menyatukan rantai lintas pesan). Kunci deterministik:
// referensi pertama rantai (root) bila ada header reply; kalau bukan kunci
// subject terikat folder "s<folderID>:<subject>" — fallback hanya untuk pesan
// tanpa header reply sama sekali. Kosong bila tidak ada identitas sama sekali.
func ThreadSeedKey(folderID int64, messageID, inReplyTo, refs, subject string) string {
	chain := splitMessageIDs(refs)
	if len(chain) == 0 {
		chain = splitMessageIDs(inReplyTo)
	}
	if len(chain) > 0 {
		return chain[0]
	}
	norm := NormalizeThreadSubject(subject)
	if norm == "" {
		return ""
	}
	return fmt.Sprintf("s%d:%s", folderID, norm)
}

// Jenis kunci union-find: 'm' Message-ID, 's' fallback subject per-folder,
// 'u' singleton unik (pesan tanpa identitas apa pun).
type keyKind byte

const (
	keyMsgID   keyKind = 'm'
	keySubject keyKind = 's'
	keyUnique  keyKind = 'u'
)

// threadRow adalah satu baris pesan untuk penghitungan ulang thread.
type threadRow struct {
	id      int64
	keys    []string // kunci union-find milik pesan ini (≥1)
	msgID   string   // Message-ID dinormalisasi ("" bila tidak ada)
	hasReply bool    // punya header References/In-Reply-To
	thread  string   // thread_id hasil
	current string   // thread_id tersimpan saat ini
}

// RegroupThreads menghitung ulang thread_id seluruh pesan folder secara
// atomik: pesan terhubung bila saling merujuk lewat References/In-Reply-To
// (union-find atas Message-ID dinormalisasi); pesan tanpa header reply
// digabung lewat subject dinormalisasi (terikat folder). thread_id komponen =
// Message-ID normalized terkecil di komponen (atau kunci fallback terkecil
// bila tak ada anggota ber-Message-ID) — deterministik terhadap urutan masuk.
func (db *DB) RegroupThreads(folderID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai tx regroup folder %d: %w", folderID, err)
	}
	defer tx.Rollback()
	if err := regroupThreadsTx(tx, folderID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit regroup folder %d: %w", folderID, err)
	}
	return nil
}

func regroupThreadsTx(tx *sql.Tx, folderID int64) error {
	// Union-find atas seluruh kunci thread; root komponen = kunci
	// leksikografis terkecil agar deterministik.
	parent := map[string]string{}
	kinds := map[string]keyKind{}
	node := func(k string, kk keyKind) {
		if _, ok := parent[k]; !ok {
			parent[k] = k
			kinds[k] = kk
		}
	}
	find := func(k string) string {
		for parent[k] != k {
			parent[k] = parent[parent[k]]
			k = parent[k]
		}
		return k
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}

	rows, err := tx.Query(`SELECT id, COALESCE(message_id,''), COALESCE(in_reply_to,''),
		COALESCE(refs,''), COALESCE(subject,''), COALESCE(thread_id,'')
		FROM messages WHERE folder_id=?`, folderID)
	if err != nil {
		return fmt.Errorf("baca pesan folder %d: %w", folderID, err)
	}
	var trs []threadRow
	for rows.Next() {
		var r threadRow
		var mid, irt, refs, subj string
		if err := rows.Scan(&r.id, &mid, &irt, &refs, &subj, &r.current); err != nil {
			rows.Close()
			return fmt.Errorf("baca baris pesan folder %d: %w", folderID, err)
		}
		seen := map[string]bool{}
		r.msgID = NormalizeMessageID(mid)
		addKey := func(k string, kk keyKind) {
			if k != "" && !seen[k] {
				seen[k] = true
				r.keys = append(r.keys, k)
				node(k, kk)
			}
		}
		// Kunci identitas: seluruh referensi ancestor (References lalu
		// In-Reply-To), duplikat & self-reference dibuang.
		for _, p := range splitMessageIDs(refs) {
			addKey(p, keyMsgID)
		}
		for _, p := range splitMessageIDs(irt) {
			addKey(p, keyMsgID)
		}
		r.hasReply = len(r.keys) > 0
		if r.msgID != "" {
			addKey(r.msgID, keyMsgID)
		}
		if !r.hasReply {
			// Fallback subject hanya untuk pesan tanpa header reply sama
			// sekali; pesan tanpa identitas apa pun dibuat singleton.
			if k := ThreadSeedKey(folderID, mid, irt, refs, subj); k != "" {
				addKey(k, keySubject)
			} else {
				addKey(fmt.Sprintf("u%d", r.id), keyUnique)
			}
		}
		if len(r.keys) == 0 {
			addKey(fmt.Sprintf("u%d", r.id), keyUnique)
		}
		trs = append(trs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterasi pesan folder %d: %w", folderID, err)
	}
	rows.Close()
	if len(trs) == 0 {
		return nil
	}
	for i := range trs {
		for _, k := range trs[i].keys {
			union(trs[i].keys[0], k)
		}
	}

	// thread_id komponen: kunci Message-ID terkecil di komponen; bila tidak
	// ada kunci jenis itu, kunci fallback terkecil.
	type cand struct {
		key  string
		kind keyKind
	}
	canon := map[string]cand{}
	for k, kk := range kinds {
		root := find(k)
		c, ok := canon[root]
		switch {
		case !ok:
			canon[root] = cand{k, kk}
		case kk == keyMsgID && c.kind != keyMsgID,
			kk == c.kind && k < c.key:
			canon[root] = cand{k, kk}
		}
	}

	for i := range trs {
		trs[i].thread = canon[find(trs[i].keys[0])].key
		if trs[i].thread == trs[i].current {
			continue
		}
		if _, err := tx.Exec(`UPDATE messages SET thread_id=? WHERE id=?`,
			trs[i].thread, trs[i].id); err != nil {
			return fmt.Errorf("simpan thread_id pesan %d: %w", trs[i].id, err)
		}
	}
	return nil
}
