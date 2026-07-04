package main

import (
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─────────────────────────────────────────
//  ADMIN EDIT "MY LORD" PANEL
//
//  Server ini jalan di port terpisah (lihat main.go: PortAdminEdit).
//  Dipakai admin untuk: aktifkan/suspend akun member, upload "database"
//  aset situs (11 kategori), dan lihat inventaris per kategori.
// ─────────────────────────────────────────

//go:embed web/admin_edit_login.html
var adminLoginHTML []byte

//go:embed web/admin_edit_panel.html
var adminPanelHTML []byte

// AdminUploadDir tempat fisik file "Menu Upload Database" disimpan.
var AdminUploadDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "admin_uploads")
}()

// ─────────────────────────────────────────
//  MIDDLEWARE AUTH ADMIN
// ─────────────────────────────────────────

func authAdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_token")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		var userID int
		var expires int64
		err = DB.QueryRow(
			"SELECT user_id, expires_at FROM sessions_admin WHERE token=?", cookie.Value,
		).Scan(&userID, &expires)
		if err != nil || time.Now().Unix() > expires {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// ─────────────────────────────────────────
//  ROUTES
// ─────────────────────────────────────────

func RegisterAdminEditRoutes(mux *http.ServeMux) {
	os.MkdirAll(AdminUploadDir, 0755)

	mux.HandleFunc("/", authAdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(adminPanelHTML)
	}))
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(adminLoginHTML)
	})
	mux.Handle("/admin_uploads/", http.StripPrefix("/admin_uploads/",
		http.FileServer(http.Dir(AdminUploadDir))))

	mux.HandleFunc("/api/login", apiAdminLogin)
	mux.HandleFunc("/api/logout", apiAdminLogout)
	mux.HandleFunc("/api/profile/members", authAdminMiddleware(apiAdminMembers))
	mux.HandleFunc("/api/profile/suspend", authAdminMiddleware(apiAdminSuspend))
	mux.HandleFunc("/api/profile/aktifkan", authAdminMiddleware(apiAdminAktifkan))
	mux.HandleFunc("/api/assets", authAdminMiddleware(apiAdminAssetsList))
	mux.HandleFunc("/api/assets/upload", authAdminMiddleware(apiAdminAssetsUpload))
	mux.HandleFunc("/api/inventaris/", authAdminMiddleware(apiAdminInventaris))
}

// ─────────────────────────────────────────
//  LOGIN / LOGOUT
// ─────────────────────────────────────────

func apiAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	readJSON(r, &body)

	var userID int
	err := DB.QueryRow(
		`SELECT id FROM users WHERE username=? AND password=SHA2(?,256) AND role='admin'`,
		body.Username, body.Password,
	).Scan(&userID)
	if err != nil {
		sendJSON(w, 401, map[string]string{"error": "Username atau password salah"})
		return
	}

	token := generateToken()
	expires := time.Now().Add(24 * time.Hour).Unix()
	DB.Exec("INSERT INTO sessions_admin (token, user_id, expires_at) VALUES (?,?,?)",
		token, userID, expires)

	http.SetCookie(w, &http.Cookie{
		Name:    "admin_token",
		Value:   token,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})
	sendJSON(w, 200, map[string]string{"message": "Login berhasil"})
}

func apiAdminLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("admin_token")
	if err == nil {
		DB.Exec("DELETE FROM sessions_admin WHERE token=?", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_token", Value: "", MaxAge: -1, Path: "/"})
	sendJSON(w, 200, map[string]string{"message": "Logout berhasil"})
}

// ─────────────────────────────────────────
//  MEMBER: list / suspend / aktifkan
// ─────────────────────────────────────────

type adminMemberRow struct {
	ID               int    `json:"id"`
	Username         string `json:"username"`
	StatusAkun       string `json:"status_akun"`
	Nama             string `json:"nama"`
	Status           string `json:"status"`
	KTA              string `json:"kta"`
	TanggalBergabung string `json:"tanggal_bergabung"`
	FotoProfil       string `json:"foto_profil"`
}

func apiAdminMembers(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query(`
		SELECT u.id, u.username, u.status_akun,
		       COALESCE(m.nama, ''), COALESCE(m.status, ''), COALESCE(m.kta, ''),
		       COALESCE(m.tanggal_bergabung, ''), COALESCE(mp.aksesori_foto, '')
		FROM users u
		LEFT JOIN member_profile mp ON mp.username = u.username
		LEFT JOIN members m ON m.id = mp.member_id
		WHERE u.role != 'admin'
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var list []adminMemberRow
	for rows.Next() {
		var m adminMemberRow
		rows.Scan(&m.ID, &m.Username, &m.StatusAkun, &m.Nama, &m.Status, &m.KTA,
			&m.TanggalBergabung, &m.FotoProfil)
		list = append(list, m)
	}
	if list == nil {
		list = []adminMemberRow{}
	}
	sendJSON(w, 200, list)
}

func apiAdminSuspend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == 0 {
		sendJSON(w, 400, map[string]string{"error": "ID tidak valid"})
		return
	}
	DB.Exec("UPDATE users SET status_akun='cekal' WHERE id=?", body.ID)
	sendJSON(w, 200, map[string]string{"message": "Member disuspend"})
}

func apiAdminAktifkan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == 0 {
		sendJSON(w, 400, map[string]string{"error": "ID tidak valid"})
		return
	}

	var username string
	if err := DB.QueryRow("SELECT username FROM users WHERE id=?", body.ID).Scan(&username); err != nil {
		sendJSON(w, 404, map[string]string{"error": "User tidak ditemukan"})
		return
	}
	DB.Exec("UPDATE users SET status_akun='aktif' WHERE id=?", body.ID)

	// Kalau belum punya data member (KTA), buat baru sekalian.
	var memberID int
	err := DB.QueryRow(`
		SELECT m.id FROM member_profile mp JOIN members m ON m.id = mp.member_id
		WHERE mp.username=?`, username).Scan(&memberID)

	kta := ""
	if err != nil {
		kta = fmt.Sprintf("KTA-%d-%d", body.ID, time.Now().Unix()%100000)
		res, errIns := DB.Exec(
			`INSERT INTO members (nama, status, tanggal_bergabung, kta) VALUES (?, 'Kohai', CURDATE(), ?)`,
			username, kta)
		if errIns == nil {
			newID, _ := res.LastInsertId()
			DB.Exec(`INSERT INTO member_profile (member_id, username) VALUES (?, ?)`, newID, username)
		}
	} else {
		DB.QueryRow("SELECT kta FROM members WHERE id=?", memberID).Scan(&kta)
	}

	sendJSON(w, 200, map[string]string{
		"message":  "Member diaktifkan",
		"username": username,
		"kta":      kta,
	})
}

// ─────────────────────────────────────────
//  ASSETS: "Menu Upload Database" (site_assets)
// ─────────────────────────────────────────

func apiAdminAssetsList(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query("SELECT category, COALESCE(file_path,'') FROM site_assets")
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	type item struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	var list []item
	for rows.Next() {
		var it item
		rows.Scan(&it.Key, &it.Path)
		list = append(list, it)
	}
	if list == nil {
		list = []item{}
	}
	sendJSON(w, 200, list)
}

func apiAdminAssetsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	r.ParseMultipartForm(50 << 20)

	category := r.FormValue("category")
	if category == "" {
		sendJSON(w, 400, map[string]string{"error": "Kategori tidak valid"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "File tidak ditemukan"})
		return
	}
	defer file.Close()

	filename := strings.ReplaceAll(header.Filename, " ", "_")
	saveName := time.Now().Format("20060102_150405_") + filename

	catDir := filepath.Join(AdminUploadDir, category)
	os.MkdirAll(catDir, 0755)

	dst, err := os.Create(filepath.Join(catDir, saveName))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal simpan file"})
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	urlPath := "/admin_uploads/" + category + "/" + saveName
	_, err = DB.Exec(`
		INSERT INTO site_assets (category, file_path) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE file_path=VALUES(file_path), updated_at=CURRENT_TIMESTAMP`,
		category, urlPath)
	if err != nil {
		log.Printf("[AdminEdit] DB error: %v", err)
	}

	sendJSON(w, 201, map[string]any{
		"message": "Kirim berhasil",
		"path":    urlPath,
	})
}

// ─────────────────────────────────────────
//  INVENTARIS: /api/inventaris/{key}
// ─────────────────────────────────────────

func apiAdminInventaris(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/inventaris/")
	if key == "" {
		sendJSON(w, 400, map[string]string{"error": "Kategori tidak valid"})
		return
	}

	type item struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Tanggal string `json:"tanggal"`
	}
	var list []item

	if key == "musik" {
		rows, err := DB.Query(
			"SELECT title, file_path, uploaded_at FROM room_music ORDER BY uploaded_at DESC LIMIT 10")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var it item
				var t time.Time
				rows.Scan(&it.Name, &it.Path, &t)
				it.Tanggal = t.Format("2006-01-02")
				list = append(list, it)
			}
		}
	} else {
		var path, updated string
		err := DB.QueryRow(
			"SELECT COALESCE(file_path,''), COALESCE(updated_at,'') FROM site_assets WHERE category=?",
			key,
		).Scan(&path, &updated)
		if err == nil && path != "" {
			list = append(list, item{
				Name:    path[strings.LastIndex(path, "/")+1:],
				Path:    path,
				Tanggal: updated,
			})
		}
	}

	if list == nil {
		list = []item{}
	}
	sendJSON(w, 200, list)
}
