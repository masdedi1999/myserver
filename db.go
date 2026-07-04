package main

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

const DSN = "root@tcp(127.0.0.1:3306)/myserver?parseTime=true&charset=utf8mb4"

func InitDB() {
	var err error
	DB, err = sql.Open("mysql", DSN)
	if err != nil {
		log.Fatalf("[DB] Gagal buka koneksi: %v", err)
	}
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(5 * time.Minute)

	if err = DB.Ping(); err != nil {
		log.Fatalf("[DB] Gagal ping MySQL: %v\nPastikan MariaDB sudah jalan!", err)
	}
	log.Println("[DB] Terhubung ke MySQL/MariaDB ✅")
	createTables()
}

func CloseDB() {
	if DB != nil {
		DB.Close()
	}
}

func createTables() {
	queries := []string{
		// ── Users ──
		`CREATE TABLE IF NOT EXISTS users (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			username    VARCHAR(100) UNIQUE NOT NULL,
			password    VARCHAR(255) NOT NULL,
			email       VARCHAR(255),
			role        VARCHAR(20) DEFAULT 'user',
			scope       VARCHAR(20) DEFAULT 'upload',
			status_akun VARCHAR(20) DEFAULT 'pending',
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Sessions Storage ──
		`CREATE TABLE IF NOT EXISTS sessions_storage (
			token      VARCHAR(64) PRIMARY KEY,
			user_id    INT NOT NULL,
			expires_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Sessions E-Learning ──
		`CREATE TABLE IF NOT EXISTS sessions_elearning (
			token      VARCHAR(64) PRIMARY KEY,
			user_id    INT NOT NULL,
			expires_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Files ──
		`CREATE TABLE IF NOT EXISTS files (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			filename    VARCHAR(255) NOT NULL,
			category    VARCHAR(50) NOT NULL,
			path        TEXT NOT NULL,
			uploaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Chat ──
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			user_id    INT NOT NULL,
			username   VARCHAR(100) NOT NULL,
			message    TEXT NOT NULL,
			image_path TEXT,
			deleted    TINYINT(1) DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── E-Learning Kategori ──
		`CREATE TABLE IF NOT EXISTS el_categories (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			nama       VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── E-Learning Soal ──
		`CREATE TABLE IF NOT EXISTS el_questions (
			id            INT AUTO_INCREMENT PRIMARY KEY,
			category_id   INT NOT NULL,
			audio_path    TEXT,
			pertanyaan    TEXT NOT NULL,
			pilihan_a     TEXT NOT NULL,
			pilihan_b     TEXT NOT NULL,
			pilihan_c     TEXT NOT NULL,
			pilihan_d     TEXT NOT NULL,
			jawaban_benar CHAR(1) NOT NULL,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (category_id) REFERENCES el_categories(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Members ──
		`CREATE TABLE IF NOT EXISTS members (
			id                INT AUTO_INCREMENT PRIMARY KEY,
			nama              VARCHAR(255) NOT NULL,
			status            VARCHAR(50) DEFAULT 'Kohai',
			status_override   TINYINT(1) DEFAULT 0,
			lencana           JSON,
			trofi             JSON,
			tanggal_bergabung DATE NOT NULL,
			kta               VARCHAR(30) UNIQUE NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Suspicious ──
		`CREATE TABLE IF NOT EXISTS suspicious (
			id     INT AUTO_INCREMENT PRIMARY KEY,
			ip     VARCHAR(50) NOT NULL,
			domain VARCHAR(50) NOT NULL,
			jenis  VARCHAR(100) NOT NULL,
			detail TEXT,
			waktu  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── WebRTC Rooms ──
		`CREATE TABLE IF NOT EXISTS webrtc_rooms (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			room_id    VARCHAR(100) UNIQUE NOT NULL,
			room_type  VARCHAR(20) DEFAULT 'call',
			created_by INT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			closed_at  TIMESTAMP NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Sessions Admin Edit (Panel "My Lord") ──
		`CREATE TABLE IF NOT EXISTS sessions_admin (
			token      VARCHAR(64) PRIMARY KEY,
			user_id    INT NOT NULL,
			expires_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Room Music (mp3 diupload Room Master, file fisik disimpan di server) ──
		`CREATE TABLE IF NOT EXISTS room_music (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			room_id     VARCHAR(100) NOT NULL,
			title       VARCHAR(255) NOT NULL,
			file_path   TEXT NOT NULL,
			uploaded_by VARCHAR(100) NOT NULL,
			uploaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_room_music_room (room_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Member Profile Extra (dikelola lewat panel "My Lord") ──
		`CREATE TABLE IF NOT EXISTS member_profile (
			member_id     INT PRIMARY KEY,
			username      VARCHAR(100),
			popup_text    TEXT,
			icon_lencana  TEXT,
			file_pdf      TEXT,
			emoticon      TEXT,
			aksesori_foto TEXT,
			updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			FOREIGN KEY (member_id) REFERENCES members(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Site Assets (menu "Upload Database" global, 11 kategori) ──
		`CREATE TABLE IF NOT EXISTS site_assets (
			category   VARCHAR(50) PRIMARY KEY,
			file_path  TEXT,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		// ── Liga Stats: rekap menang/kalah per member, per liga, per tier ──
		// (emas/perak/perunggu) — dipakai untuk isi trofi & detail La Liga
		// secara dinamis di panel "My Personality". Diupdate lewat
		// /api/liga/hasil setiap kali sebuah sesi liga selesai.
		`CREATE TABLE IF NOT EXISTS liga_stats (
			member_id INT NOT NULL,
			liga      VARCHAR(50) NOT NULL,
			tier      VARCHAR(20) NOT NULL,
			menang    INT NOT NULL DEFAULT 0,
			kalah     INT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (member_id, liga, tier),
			FOREIGN KEY (member_id) REFERENCES members(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}

	for _, q := range queries {
		if _, err := DB.Exec(q); err != nil {
			log.Printf("[DB] Warning create table: %v", err)
		}
	}

	migrateColumns()

	// Insert admin default (akun admin langsung aktif, tidak perlu konfirmasi)
	_, _ = DB.Exec(`
		INSERT IGNORE INTO users (username, password, role, scope, status_akun)
		VALUES ('admin', SHA2('rahasia123', 256), 'admin', 'all', 'aktif')
	`)
	// Jaga-jaga untuk instalasi lama: pastikan akun admin/scope-all tidak
	// ikut tersangkut status 'pending' akibat penambahan kolom status_akun.
	DB.Exec(`UPDATE users SET status_akun='aktif' WHERE (role='admin' OR scope='all') AND status_akun<>'aktif'`)
	log.Println("[DB] Semua tabel siap ✅")
}

// migrateColumns menambahkan kolom yang mungkin belum ada di tabel lama
// (kasus: tabel sudah pernah dibuat sebelum kolom ini ditambahkan ke kode,
// sehingga CREATE TABLE IF NOT EXISTS tidak menambahkannya).
func migrateColumns() {
	type col struct {
		table, column, ddl string
	}
	cols := []col{
		{"users", "role", "ALTER TABLE users ADD COLUMN role VARCHAR(20) DEFAULT 'user'"},
		{"users", "scope", "ALTER TABLE users ADD COLUMN scope VARCHAR(20) DEFAULT 'upload'"},
		{"users", "status_akun", "ALTER TABLE users ADD COLUMN status_akun VARCHAR(20) DEFAULT 'pending'"},
		{"users", "email", "ALTER TABLE users ADD COLUMN email VARCHAR(255)"},
	}
	for _, c := range cols {
		var exists int
		err := DB.QueryRow(`
			SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`,
			c.table, c.column,
		).Scan(&exists)
		if err == nil && exists == 0 {
			if _, err := DB.Exec(c.ddl); err != nil {
				log.Printf("[DB] Gagal migrasi kolom %s.%s: %v", c.table, c.column, err)
			} else {
				log.Printf("[DB] Kolom %s.%s ditambahkan ✅", c.table, c.column)
			}
		}
	}
}
