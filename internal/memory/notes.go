package memory

// G2 — Notes "Obsidian-style": catatan berkategori + wiki-link antar catatan.
// - notes: judul (slug unik), kategori, isi markdown (dapat berisi [[link]])
// - note_links: graf antar catatan (dihitung ulang saat simpan — sederhana & aman)
// Pencarian: LIKE (cukup untuk ratusan catatan di N2600).

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Note satu catatan.
type Note struct {
	ID        int64
	Slug      string // judul normalisasi, unik, dipakai untuk [[link]]
	Title     string
	Category  string
	Content   string
	CreatedAt string
	UpdatedAt string
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)
var wikiLinkRe = regexp.MustCompile(`\[\[([^\[\]]{1,120})\]\]`)

// ensureNotes buat tabel (idempotent).
func (s *Store) ensureNotes() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  slug TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  created_at TEXT DEFAULT (datetime('now')),
  updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notes_cat ON notes(category);
CREATE TABLE IF NOT EXISTS note_links (
  from_id INTEGER NOT NULL,
  to_slug TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_note_links_from ON note_links(from_id);
`)
	return err
}

// CutRunes potong string aman-rune (maks n rune, tidak membelah UTF-8).
func CutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Slugify ubah judul bebas → slug unik-friendly (huruf kecil, tanda hubung).
func Slugify(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	// pertahankan karakter non-ascii (Indonesia aman karena latin)
	var b strings.Builder
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 127: // aksara lain diterima apa adanya
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := slugRe.ReplaceAllString(b.String(), "-")
	out = strings.Trim(out, "-")
	if out == "" {
		out = "catatan"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// UpsertNote simpan catatan (baru atau perbarui by slug) + hitung ulang links.
// Kembalikan (note, created) — created=true jika catatan baru.
func (s *Store) UpsertNote(title, category, content string) (*Note, bool, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, false, fmt.Errorf("judul kosong")
	}
	title = CutRunes(title, 200)
	if len(content) > 1<<20 { // 1 MB per catatan — jaga DB tetap ramping
		return nil, false, fmt.Errorf("isi catatan terlalu besar (maks 1 MB)")
	}
	slug := Slugify(title)
	category = strings.TrimSpace(category)
	category = CutRunes(category, 60)

	var id int64
	var created bool
	err := s.db.QueryRow(`SELECT id FROM notes WHERE slug=?`, slug).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := s.db.Exec(
			`INSERT INTO notes(slug,title,category,content) VALUES(?,?,?,?)`,
			slug, title, category, content)
		if err != nil {
			return nil, false, err
		}
		id, _ = res.LastInsertId()
		created = true
	case err != nil:
		return nil, false, err
	default:
		_, err := s.db.Exec(
			`UPDATE notes SET title=?, category=?, content=?, updated_at=datetime('now') WHERE id=?`,
			title, category, content, id)
		if err != nil {
			return nil, false, err
		}
	}

	// graf tautan: hitung ulang (DELETE+INSERT dalam satu transaksi kecil)
	links := wikiLinkRe.FindAllStringSubmatch(content, -1)
	if tx, err := s.db.Begin(); err == nil {
		if _, err := tx.Exec(`DELETE FROM note_links WHERE from_id=?`, id); err != nil {
			tx.Rollback()
		} else {
			seen := map[string]bool{}
			ok := true
			for _, m := range links {
				target := Slugify(m[1])
				if target == slug || seen[target] { // self-link & duplikat diabaikan
					continue
				}
				seen[target] = true
				if _, err := tx.Exec(`INSERT INTO note_links(from_id,to_slug) VALUES(?,?)`, id, target); err != nil {
					ok = false
					break
				}
			}
			if ok {
				tx.Commit()
			} else {
				tx.Rollback()
			}
		}
	}

	n, err := s.GetNoteBySlug(slug)
	return n, created, err
}

// GetNoteBySlug ambil satu catatan.
func (s *Store) GetNoteBySlug(slug string) (*Note, error) {
	row := s.db.QueryRow(
		`SELECT id,slug,title,category,content,created_at,updated_at FROM notes WHERE slug=?`, slug)
	return scanNote(row)
}

// GetNoteByID ambil catatan by id (untuk tombol).
func (s *Store) GetNoteByID(id int64) (*Note, error) {
	row := s.db.QueryRow(
		`SELECT id,slug,title,category,content,created_at,updated_at FROM notes WHERE id=?`, id)
	return scanNote(row)
}

// SearchNotes cari di judul+isi+kategori.
func (s *Store) SearchNotes(q string, limit int) []Note {
	if limit <= 0 {
		limit = 10
	}
	like := "%" + strings.ReplaceAll(strings.ReplaceAll(q, "%", ""), "_", "") + "%"
	return s.noteRows(
		`SELECT id,slug,title,category,content,created_at,updated_at FROM notes
		 WHERE title LIKE ? OR content LIKE ? OR category LIKE ?
		 ORDER BY updated_at DESC LIMIT ?`, like, like, like, limit)
}

// NotesByCategory daftar catatan satu kategori.
func (s *Store) NotesByCategory(category string, limit int) []Note {
	if limit <= 0 {
		limit = 30
	}
	return s.noteRows(
		`SELECT id,slug,title,category,content,created_at,updated_at FROM notes
		 WHERE category LIKE ? ORDER BY updated_at DESC LIMIT ?`,
		"%"+strings.ReplaceAll(category, "%", "")+"%", limit)
}

// Categories daftar kategori + jumlah catatan.
func (s *Store) Categories() []struct {
	Name  string
	Count int
} {
	rows, err := s.db.Query(
		`SELECT category, COUNT(*) FROM notes GROUP BY category ORDER BY COUNT(*) DESC LIMIT 50`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []struct {
		Name  string
		Count int
	}
	for rows.Next() {
		var name string
		var c int
		if err := rows.Scan(&name, &c); err == nil {
			out = append(out, struct {
				Name  string
				Count int
			}{name, c})
		}
	}
	return out
}

// LinkedNotes catatan yang menunjuk ke slug ini (backlink) & yang dirujuknya.
func (s *Store) LinkedNotes(slug string) (backlinks, forward []string) {
	// backlink: note_links.to_slug = slug → judul catatan sumber
	rows, err := s.db.Query(
		`SELECT n.title FROM note_links l JOIN notes n ON n.id=l.from_id WHERE l.to_slug=? LIMIT 20`, slug)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				backlinks = append(backlinks, t)
			}
		}
	}
	// forward: dari note ini
	row := s.db.QueryRow(`SELECT id FROM notes WHERE slug=?`, slug)
	var id int64
	if err := row.Scan(&id); err != nil {
		return backlinks, nil
	}
	rows2, err := s.db.Query(
		`SELECT n.title FROM note_links l JOIN notes n ON n.slug=l.to_slug WHERE l.from_id=? LIMIT 20`, id)
	if err != nil {
		return backlinks, forward
	}
	defer rows2.Close()
	for rows2.Next() {
		var t string
		if rows2.Scan(&t) == nil {
			forward = append(forward, t)
		}
	}
	return backlinks, forward
}

// DeleteNote hapus catatan + tautannya.
func (s *Store) DeleteNote(slug string) error {
	row := s.db.QueryRow(`SELECT id FROM notes WHERE slug=?`, slug)
	var id int64
	if err := row.Scan(&id); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	tx.Exec(`DELETE FROM notes WHERE id=?`, id)
	tx.Exec(`DELETE FROM note_links WHERE from_id=?`, id)
	return tx.Commit()
}

// NoteCount jumlah catatan (untuk stats).
func (s *Store) NoteCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&n)
	return n
}

// ---------- helpers ----------

func scanNote(row interface{ Scan(...interface{}) error }) (*Note, error) {
	n := &Note{}
	var cat sql.NullString
	err := row.Scan(&n.ID, &n.Slug, &n.Title, &cat, &n.Content, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	n.Category = cat.String
	return n, nil
}

func (s *Store) noteRows(query string, args ...interface{}) []Note {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err == nil {
			out = append(out, *n)
		}
	}
	return out
}

