// Package memory — F5: persisten memory + session (SQLite FTS5, pure-Go modernc.org/sqlite).
// RAM-footprint kecil: satu koneksi, query terbatas.
package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store memory bot.
type Store struct {
	db *sql.DB
}

// Open membuka/membuat DB + skema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	// 1 koneksi cukup (hemat RAM, hindari lock)
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key TEXT UNIQUE,
  content TEXT NOT NULL,
  created_at TEXT DEFAULT (datetime('now')),
  updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS session (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  role TEXT NOT NULL,          -- user|assistant
  content TEXT NOT NULL,
  ts TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_session_chat ON session(chat_id, id DESC);
CREATE TABLE IF NOT EXISTS lessons (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  topic TEXT NOT NULL UNIQUE,
  lesson TEXT NOT NULL,
  hits INTEGER DEFAULT 0,
  confirmed INTEGER DEFAULT 0,  -- review threshold F5c: 1 = sudah dikonfirmasi
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS chat_summary (
  chat_id INTEGER PRIMARY KEY,
  summary TEXT NOT NULL,
  updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS memory_hits (
  key TEXT PRIMARY KEY,
  hits INTEGER DEFAULT 0,
  last_hit TEXT DEFAULT (datetime('now'))
);
`)
	if err != nil {
		return err
	}
	// uploads (G1 upload & baca file) — idempotent
	if err := s.ensureUploads(); err != nil {
		return err
	}
	// notes (G2 notes obsidian) — idempotent
	if err := s.ensureNotes(); err != nil {
		return err
	}
	// migrasi DB lama: lessons.topic belum UNIQUE → buat ulang dengan constraint.
	// Aman dijalankan berkali-kali (idempotent).
	var pk string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='lessons'`).Scan(&pk); err == nil &&
		!strings.Contains(pk, "UNIQUE") {
		tx, terr := s.db.Begin()
		if terr != nil {
			return terr
		}
		steps := []string{
			`ALTER TABLE lessons RENAME TO lessons_old`,
			`CREATE TABLE lessons (
			  id INTEGER PRIMARY KEY AUTOINCREMENT,
			  topic TEXT NOT NULL UNIQUE,
			  lesson TEXT NOT NULL,
			  hits INTEGER DEFAULT 0,
			  confirmed INTEGER DEFAULT 0,
			  created_at TEXT DEFAULT (datetime('now'))
			)`,
			`INSERT INTO lessons(id, topic, lesson, hits, confirmed, created_at)
			   SELECT id, topic, lesson, hits, confirmed, created_at FROM lessons_old`,
			`DROP TABLE lessons_old`,
		}
		for _, q := range steps {
			if _, err := tx.Exec(q); err != nil {
				tx.Rollback()
				return fmt.Errorf("migrasi lessons: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return err
}

// Close menutup DB.
func (s *Store) Close() error { return s.db.Close() }

// ---- Memory (key-value + FTS cari) ----

// Put simpan/replace memory per key.
func (s *Store) Put(key, content string) error {
	_, err := s.db.Exec(`INSERT INTO memory(key, content) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET content=excluded.content, updated_at=datetime('now')`, key, content)
	return err
}

// Get ambil memory per key.
func (s *Store) Get(key string) (string, bool) {
	var c string
	err := s.db.QueryRow(`SELECT content FROM memory WHERE key=?`, key).Scan(&c)
	if err != nil {
		return "", false
	}
	return c, true
}

// Delete hapus memory per key.
func (s *Store) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM memory WHERE key=?`, key)
	return err
}

// Search cari memory (LIKE sederhana — cukup utk skala kecil, FTS5 opsional).
func (s *Store) Search(q string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	like := "%" + strings.ReplaceAll(q, "%", "") + "%"
	rows, err := s.db.Query(
		`SELECT key || ': ' || substr(content,1,300) FROM memory
		 WHERE content LIKE ? OR key LIKE ? ORDER BY updated_at DESC LIMIT ?`, like, like, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			out = append(out, line)
		}
	}
	return out
}

// ---- Session (riwayat percakapan per chat) ----

// AppendSession simpan 1 pesan ke riwayat chat.
func (s *Store) AppendSession(chatID int64, role, content string) {
	s.db.Exec(`INSERT INTO session(chat_id, role, content) VALUES(?,?,?)`, chatID, role, content)
	// prune: simpan 100 pesan terakhir per chat
	s.db.Exec(`DELETE FROM session WHERE chat_id=? AND id NOT IN
		(SELECT id FROM session WHERE chat_id=? ORDER BY id DESC LIMIT 100)`, chatID, chatID)
}

// RecentSession ambil N pesan terakhir chat (urut lama→baru).
func (s *Store) RecentSession(chatID int64, n int) []string {
	if n <= 0 {
		n = 6
	}
	rows, err := s.db.Query(`SELECT role || ': ' || substr(content,1,200) FROM
		(SELECT role, content FROM session WHERE chat_id=? ORDER BY id DESC LIMIT ?)
		ORDER BY id ASC`, chatID, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			out = append(out, line)
		}
	}
	return out
}

// ---- Lessons (F5c auto-lesson, dengan review threshold) ----

// AddLesson simpan lesson; kalau topic sudah ada, update content & reset confirmed.
func (s *Store) AddLesson(topic, lesson string) error {
	_, err := s.db.Exec(`INSERT INTO lessons(topic, lesson) VALUES(?,?)
		ON CONFLICT(topic) DO UPDATE SET lesson=excluded.lesson, confirmed=0, created_at=datetime('now')`,
		topic, lesson)
	return err
}

// Lessons mengambil lesson terkonfirmasi (dipakai di prompt) — Maks 10.
func (s *Store) Lessons() []string {
	rows, err := s.db.Query(`SELECT topic || ': ' || lesson FROM lessons
		WHERE confirmed=1 ORDER BY hits DESC, id DESC LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			out = append(out, line)
		}
	}
	return out
}

// PendingLessons lesson yang menunggu konfirmasi (untuk /lesson review).
func (s *Store) PendingLessons() []string {
	rows, err := s.db.Query(`SELECT id || '. [' || topic || '] ' || lesson FROM lessons WHERE confirmed=0 ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			out = append(out, line)
		}
	}
	return out
}

// ConfirmLesson setujui lesson (hit +1 juga).
func (s *Store) ConfirmLesson(id int) error {
	_, err := s.db.Exec(`UPDATE lessons SET confirmed=1, hits=hits+1 WHERE id=?`, id)
	return err
}

// DeleteLesson hapus lesson.
func (s *Store) DeleteLesson(id int) error {
	_, err := s.db.Exec(`DELETE FROM lessons WHERE id=?`, id)
	return err
}

// TouchLesson naikkan hits saat lesson dipakai di prompt.
func (s *Store) TouchLessons() { s.db.Exec(`UPDATE lessons SET hits=hits+1 WHERE confirmed=1`) }

// Now helper format waktu (untuk logging).
func Now() string { return time.Now().Format("2006-01-02 15:04:05") }

// ---- F6a: running summary per chat ----

// GetSummary ambil ringkasan berjalan chat.
func (s *Store) GetSummary(chatID int64) (string, bool) {
	var sm string
	err := s.db.QueryRow(`SELECT summary FROM chat_summary WHERE chat_id=?`, chatID).Scan(&sm)
	if err != nil || sm == "" {
		return "", false
	}
	return sm, true
}

// SetSummary simpan ringkasan berjalan chat.
func (s *Store) SetSummary(chatID int64, summary string) {
	s.db.Exec(`INSERT INTO chat_summary(chat_id, summary) VALUES(?,?)
		ON CONFLICT(chat_id) DO UPDATE SET summary=excluded.summary, updated_at=datetime('now')`,
		chatID, summary)
}

// ---- F6b: auto-recall & kurator ----

// AutoRecall — cari memory relevan dari kata kunci pesan user (diam-diam suntik konteks).
// Ekstrak kata unik (>=4 huruf), cari masing-masing, gabungkan hasil unik, maks 3.
func (s *Store) AutoRecall(userMsg string, max int) []string {
	if max <= 0 {
		max = 3
	}
	seen := map[string]bool{}
	var out []string
	words := strings.FieldsFunc(strings.ToLower(userMsg), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	})
	queried := map[string]bool{}
	for _, w := range words {
		if len(w) < 4 || queried[w] {
			continue
		}
		queried[w] = true
		for _, hit := range s.Search(w, 3) {
			key := hit
			if i := strings.Index(hit, ": "); i > 0 {
				key = hit[:i]
				// naikkan hit counter (untuk kurator)
				s.db.Exec(`INSERT INTO memory_hits(key, hits, last_hit) VALUES(?,1,datetime('now'))
					ON CONFLICT(key) DO UPDATE SET hits=hits+1, last_hit=datetime('now')`, key)
			}
			if !seen[hit] {
				seen[hit] = true
				out = append(out, hit)
				if len(out) >= max {
					return out
				}
			}
		}
	}
	return out
}

// Curate — kurator: hapus memory yang tak pernah di-recall >30 hari & punya hit 0.
// Return jumlah yang dihapus.
func (s *Store) Curate() int {
	res, err := s.db.Exec(`DELETE FROM memory WHERE key IN (
		SELECT key FROM memory
		LEFT JOIN memory_hits ON memory.key = memory_hits.key
		WHERE (memory_hits.last_hit IS NULL OR memory_hits.last_hit < datetime('now','-30 days'))
		  AND memory.updated_at < datetime('now','-30 days'))`)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// Stats ringkas (untuk /memory).
func (s *Store) Stats() string {
	var mem, ses, les int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory`).Scan(&mem)
	s.db.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&ses)
	s.db.QueryRow(`SELECT COUNT(*) FROM lessons WHERE confirmed=1`).Scan(&les)
	return fmt.Sprintf("🧠 %d memory · %d pesan session · %d lesson aktif", mem, ses, les)
}
