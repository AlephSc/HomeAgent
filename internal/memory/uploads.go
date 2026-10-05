package memory

import (
	"database/sql"
	"fmt"
	"strings"
)

// Upload — metadata satu file yang diupload user / dideteksi di inbox.
// Isi dokumen TIDAK disimpan di DB — hanya path (disk-first).
type Upload struct {
	ID        int64
	ChatID    int64
	Name      string // nama file tampilan
	Path      string // path file asli di disk
	TextPath  string // path hasil ekstraksi teks (.txt), kosong jika belum/tidak perlu
	Size      int64
	Pages     int    // total halaman (PDF), 0 jika bukan PDF
	PagesDone int    // halaman yang sudah terekstrak (resume checkpoint)
	Status    string // "menunggu" | "proses" | "siap" | "gagal"
	Err       string
	CreatedAt string
}

// ensureUploads membuat tabel uploads (idempotent, dipanggil dari Open via migrate).
func (s *Store) ensureUploads() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS uploads (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  text_path TEXT DEFAULT '',
  size INTEGER DEFAULT 0,
  pages INTEGER DEFAULT 0,
  pages_done INTEGER DEFAULT 0,
  status TEXT DEFAULT 'menunggu',
  err TEXT DEFAULT '',
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_uploads_chat ON uploads(chat_id, id DESC);
`)
	return err
}

// AddUpload mendaftarkan file baru, kembalikan ID.
func (s *Store) AddUpload(chatID int64, name, path string, size int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO uploads(chat_id, name, path, size, status) VALUES(?,?,?,?,?)`,
		chatID, name, path, size, "menunggu")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateUploadProgress simpan checkpoint ekstraksi (resume-safe).
func (s *Store) UpdateUploadProgress(id int64, textPath string, pagesDone, pages int, status string) {
	if status == "siap" {
		s.db.Exec(`UPDATE uploads SET text_path=?, pages_done=?, pages=?, status=?, err='' WHERE id=?`,
			textPath, pagesDone, pages, status, id)
		return
	}
	s.db.Exec(`UPDATE uploads SET text_path=?, pages_done=?, pages=?, status=? WHERE id=?`,
		textPath, pagesDone, pages, status, id)
}

// FailUpload tandai gagal + pesan errornya.
func (s *Store) FailUpload(id int64, errMsg string) {
	s.db.Exec(`UPDATE uploads SET status='gagal', err=? WHERE id=?`, errMsg, id)
}

// GetUpload by id.
func (s *Store) GetUpload(id int64) (*Upload, error) {
	row := s.db.QueryRow(`SELECT id,chat_id,name,path,text_path,size,pages,pages_done,status,err,created_at FROM uploads WHERE id=?`, id)
	return scanUpload(row)
}

// FindUploadByName pencarian longgar (substring, case-insensitive) — terbaru menang.
func (s *Store) FindUploadByName(q string) (*Upload, error) {
	q = strings.ReplaceAll(strings.TrimSpace(q), "%", "")
	row := s.db.QueryRow(
		`SELECT id,chat_id,name,path,text_path,size,pages,pages_done,status,err,created_at
		 FROM uploads WHERE lower(name) LIKE ? ORDER BY id DESC LIMIT 1`,
		"%"+strings.ToLower(q)+"%")
	return scanUpload(row)
}

// ListUploads daftar upload terbaru.
func (s *Store) ListUploads(limit int) []Upload {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id,chat_id,name,path,text_path,size,pages,pages_done,status,err,created_at
		 FROM uploads ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err == nil {
			out = append(out, *u)
		}
	}
	return out
}

// PickResume mencari upload berstatus "proses" (ekstraksi tertunda setelah restart).
func (s *Store) PickResume() []Upload {
	rows, err := s.db.Query(
		`SELECT id,chat_id,name,path,text_path,size,pages,pages_done,status,err,created_at
		 FROM uploads WHERE status='proses' ORDER BY id ASC LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err == nil {
			out = append(out, *u)
		}
	}
	return out
}

// CountStuckProses — upload "proses" yang tidak tersentuh >15 menit dianggap mati
// (mis. bot dimatikan saat ekstraksi) → biarkan PickResume yang lanjutkan.
func scanUpload(row interface{ Scan(...interface{}) error }) (*Upload, error) {
	u := &Upload{}
	var errText sql.NullString
	var createdAt sql.NullString
	err := row.Scan(&u.ID, &u.ChatID, &u.Name, &u.Path, &u.TextPath, &u.Size,
		&u.Pages, &u.PagesDone, &u.Status, &errText, &createdAt)
	if err != nil {
		return nil, err
	}
	u.Err = errText.String
	u.CreatedAt = createdAt.String
	return u, nil
}

// HumanSize format ukuran ramah.
func HumanSize(n int64) string {
	const kb, mb = 1 << 10, 1 << 20
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

var _ = fmt.Sprintf // keep imports stable
