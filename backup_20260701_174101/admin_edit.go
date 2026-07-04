package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────
//  ADMIN EDIT ("My Lord" Panel) — Port 8084
//  Edit database member secara online:
//  Username, No KTA, Pop up, Icon Lencana,
//  File PDF, Emoticon, Aksesori Foto Profil
// ─────────────────────────────────────────

var AdminUploadDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "uploads", "profile")
}()

// ── Field yang boleh diupload sebagai file, dan folder penyimpanannya ──
var adminFileFields = map[string]string{
	"icon_lencana":  "lencana",
	"file_pdf":      "pdf",
	"emoticon":      "emoticon",
	"aksesori_foto": "aksesori",
}

// ── 11 kategori menu "Upload Database" (global, bukan per-member) ──
type AssetCategory struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var assetCategories = []AssetCategory{
	{"file_pdf", "File Pdf"},
	{"emot", "Emot"},
	{"gambar", "Gambar"},
	{"popup", "Pop Up"},
	{"text_berita", "Text Berita"},
	{"background_mimbar", "Back Ground halaman mimbar bebas"},
	{"bingkai_foto", "Bingkai foto profil"},
	{"lencana", "Lencana"},
	{"trofi", "Trofi"},
	{"musik", "Musik"},
	{"bot", "Bot"},
}

var assetCategoryKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range assetCategories {
		m[c.Key] = true
	}
	return m
}()

// ── Field teks (bukan file) yang boleh diedit langsung ──
var adminTextFields = map[string]bool{
	"username":   true,
	"kta":        true, // No KTA (kolom milik tabel members)
	"popup_text": true,
}

type MemberProfile struct {
	MemberID     int    `json:"member_id"`
	Nama         string `json:"nama"`
	KTA          string `json:"kta"`
	Username     string `json:"username"`
	PopupText    string `json:"popup_text"`
	IconLencana  string `json:"icon_lencana"`
	FilePdf      string `json:"file_pdf"`
	Emoticon     string `json:"emoticon"`
	AksesoriFoto string `json:"aksesori_foto"`
}

// ─────────────────────────────────────────
//  MIDDLEWARE AUTH ADMIN EDIT
// ─────────────────────────────────────────

func authAdminEditMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Token")
		if token == "" {
			cookie, err := r.Cookie("admin_edit_token")
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			token = cookie.Value
		}
		var userID int
		var expires int64
		err := DB.QueryRow(
			"SELECT user_id, expires_at FROM sessions_admin WHERE token=?", token,
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

	mux.HandleFunc("/", authAdminEditMiddleware(func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "admin_edit_panel.html")
	}))
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "admin_edit_login.html")
	})
	mux.Handle("/uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(filepath.Join(AdminUploadDir, "..")))))

	mux.HandleFunc("/api/login", apiAdminEditLogin)
	mux.HandleFunc("/api/logout", apiAdminEditLogout)

	mux.HandleFunc("/api/profile/members", authAdminEditMiddleware(apiAdminEditMemberList))
	mux.HandleFunc("/api/profile/get", authAdminEditMiddleware(apiAdminEditProfileGet))
	mux.HandleFunc("/api/profile/text", authAdminEditMiddleware(apiAdminEditProfileText))
	mux.HandleFunc("/api/profile/upload", authAdminEditMiddleware(apiAdminEditProfileUpload))
	mux.HandleFunc("/api/profile/aktifkan", authAdminEditMiddleware(apiAdminAktifkanAkun))
	mux.HandleFunc("/api/profile/suspend", authAdminEditMiddleware(apiAdminSuspendMember))

	mux.HandleFunc("/api/assets", authAdminEditMiddleware(apiAdminAssetsList))
	mux.HandleFunc("/api/assets/upload", authAdminEditMiddleware(apiAdminAssetsUpload))

	mux.HandleFunc("/api/inventaris/", authAdminEditMiddleware(apiAdminInventaris))
}

// ─────────────────────────────────────────
//  API HANDLERS
// ─────────────────────────────────────────

// Login khusus role='admin' (atau scope='all') — yang berhak masuk panel "My Lord"
func apiAdminEditLogin(w http.ResponseWriter, r *http.Request) {
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
	var role string
	err := DB.QueryRow(
		`SELECT id, role FROM users
		 WHERE username=? AND password=SHA2(?,256)
		 AND (role='admin' OR scope='all')`,
		body.Username, body.Password,
	).Scan(&userID, &role)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[AdminEdit] Login query error: %v", err)
		}
		sendJSON(w, 401, map[string]string{"error": "Username atau password salah"})
		return
	}

	token := generateToken()
	expires := time.Now().Add(24 * time.Hour).Unix()
	DB.Exec("INSERT INTO sessions_admin (token, user_id, expires_at) VALUES (?,?,?)",
		token, userID, expires)

	http.SetCookie(w, &http.Cookie{
		Name:    "admin_edit_token",
		Value:   token,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})
	sendJSON(w, 200, map[string]string{"token": token, "message": "Welcome My Lord"})
}

func apiAdminEditLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("admin_edit_token")
	if err == nil {
		DB.Exec("DELETE FROM sessions_admin WHERE token=?", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_edit_token", Value: "", MaxAge: -1, Path: "/"})
	sendJSON(w, 200, map[string]string{"message": "Logout berhasil"})
}

// Daftar member untuk tabel pertama: No, Username, No KTA, Bergabung sejak
func apiAdminEditMemberList(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query(`
		SELECT m.id, m.nama, m.kta, m.tanggal_bergabung,
		       COALESCE(mp.username, ''), COALESCE(u.status_akun, 'aktif'),
		       COALESCE(
		           (SELECT f.path FROM files f WHERE f.category='foto_profil'
		            AND f.filename = COALESCE(mp.username,'') ORDER BY f.id DESC LIMIT 1),
		           ''
		       )
		FROM members m
		LEFT JOIN member_profile mp ON mp.member_id = m.id
		LEFT JOIN users u ON u.username = mp.username
		ORDER BY m.id`)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	type Item struct {
		ID         int    `json:"id"`
		Nama       string `json:"nama"`
		KTA        string `json:"kta"`
		Bergabung  string `json:"tanggal_bergabung"`
		Username   string `json:"username"`
		StatusAkun string `json:"status_akun"`
		FotoProfil string `json:"foto_profil"`
	}
	var list []Item
	for rows.Next() {
		var it Item
		rows.Scan(&it.ID, &it.Nama, &it.KTA, &it.Bergabung, &it.Username, &it.StatusAkun, &it.FotoProfil)
		if it.Username == "" {
			it.Username = "-"
		}
		list = append(list, it)
	}
	if list == nil {
		list = []Item{}
	}
	sendJSON(w, 200, list)
}

// Ambil profile lengkap 1 member (gabungan members + member_profile), dibuat otomatis jika belum ada
func apiAdminEditProfileGet(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "id tidak valid"})
		return
	}

	p, err := getAdminProfile(id)
	if err != nil {
		sendJSON(w, 404, map[string]string{"error": "Member tidak ditemukan"})
		return
	}
	sendJSON(w, 200, p)
}

func getAdminProfile(id int) (*MemberProfile, error) {
	var p MemberProfile
	p.MemberID = id
	err := DB.QueryRow("SELECT nama, kta FROM members WHERE id=?", id).Scan(&p.Nama, &p.KTA)
	if err != nil {
		return nil, err
	}

	// pastikan baris member_profile sudah ada (auto create)
	DB.Exec("INSERT IGNORE INTO member_profile (member_id) VALUES (?)", id)

	var username, popup, icon, pdf, emo, aksesori []byte
	row := DB.QueryRow(`SELECT username, popup_text, icon_lencana, file_pdf, emoticon, aksesori_foto
		FROM member_profile WHERE member_id=?`, id)
	if err := row.Scan(&username, &popup, &icon, &pdf, &emo, &aksesori); err != nil {
		return nil, err
	}
	p.Username = string(username)
	p.PopupText = string(popup)
	p.IconLencana = string(icon)
	p.FilePdf = string(pdf)
	p.Emoticon = string(emo)
	p.AksesoriFoto = string(aksesori)
	return &p, nil
}

// ─────────────────────────────────────────
//  AKTIVASI AKUN MEMBER
//  Saat daftar, akun disimpan dengan identitas sementara berbasis waktu
//  (mis. "01:57-26-07-2026") dan status_akun='pending'. Setelah admin
//  konfirmasi lewat panel ini, identitas itu diganti jadi username asli
//  (dibuat dari nama) dan status_akun berubah jadi 'aktif'.
// ─────────────────────────────────────────

func aktifkanAkun(memberID int) (*MemberProfile, error) {
	var nama, oldUsername string
	err := DB.QueryRow(`
		SELECT m.nama, COALESCE(mp.username, '')
		FROM members m LEFT JOIN member_profile mp ON mp.member_id = m.id
		WHERE m.id=?`, memberID).Scan(&nama, &oldUsername)
	if err != nil {
		return nil, fmt.Errorf("member tidak ditemukan")
	}
	if oldUsername == "" {
		return nil, fmt.Errorf("akun login member ini belum tersimpan")
	}

	var statusAkun string
	err = DB.QueryRow("SELECT status_akun FROM users WHERE username=?", oldUsername).Scan(&statusAkun)
	if err != nil {
		return nil, fmt.Errorf("akun login tidak ditemukan")
	}
	if statusAkun == "aktif" {
		return getAdminProfile(memberID) // sudah aktif sebelumnya, tidak perlu diubah lagi
	}

	newUsername := generateUsername(nama)

	if _, err := DB.Exec("UPDATE users SET username=?, status_akun='aktif' WHERE username=?", newUsername, oldUsername); err != nil {
		return nil, err
	}
	DB.Exec("UPDATE member_profile SET username=? WHERE member_id=?", newUsername, memberID)
	renameFotoProfil(oldUsername, newUsername)

	return getAdminProfile(memberID)
}

// renameFotoProfil memindahkan file foto profil dari nama sementara (waktu)
// ke username asli setelah akun diaktifkan, lalu menyamakan catatannya
// di tabel files supaya tetap muncul benar di halaman Inventaris.
func renameFotoProfil(oldUsername, newUsername string) {
	var path string
	err := DB.QueryRow(
		"SELECT path FROM files WHERE category='foto_profil' AND filename=? ORDER BY id DESC LIMIT 1",
		oldUsername,
	).Scan(&path)
	if err != nil {
		return // tidak ada foto tercatat, lewati
	}
	ext := filepath.Ext(path)
	oldFile := filepath.Join(FotoProfilDir, safeFileName(oldUsername)+ext)
	newFile := filepath.Join(FotoProfilDir, safeFileName(newUsername)+ext)
	if err := os.Rename(oldFile, newFile); err != nil {
		log.Printf("[AdminEdit] Gagal rename foto profil %s -> %s: %v", oldFile, newFile, err)
		return
	}
	newPath := "/uploads/foto_profil/" + safeFileName(newUsername) + ext
	DB.Exec("UPDATE files SET filename=?, path=? WHERE category='foto_profil' AND filename=?",
		newUsername, newPath, oldUsername)
}

// Update field teks: username / kta (No KTA) / popup_text
func apiAdminEditProfileText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		MemberID int    `json:"member_id"`
		Field    string `json:"field"`
		Value    string `json:"value"`
	}
	readJSON(r, &body)

	if !adminTextFields[body.Field] {
		sendJSON(w, 400, map[string]string{"error": "Field tidak valid"})
		return
	}
	body.Value = strings.TrimSpace(body.Value)

	var err error
	switch body.Field {
	case "kta":
		if body.Value == "" {
			sendJSON(w, 400, map[string]string{"error": "No KTA tidak boleh kosong"})
			return
		}
		_, err = DB.Exec("UPDATE members SET kta=? WHERE id=?", body.Value, body.MemberID)
	case "username":
		DB.Exec("INSERT IGNORE INTO member_profile (member_id) VALUES (?)", body.MemberID)
		_, err = DB.Exec("UPDATE member_profile SET username=? WHERE member_id=?", body.Value, body.MemberID)
	case "popup_text":
		DB.Exec("INSERT IGNORE INTO member_profile (member_id) VALUES (?)", body.MemberID)
		_, err = DB.Exec("UPDATE member_profile SET popup_text=? WHERE member_id=?", body.Value, body.MemberID)
	}
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	p, _ := getAdminProfile(body.MemberID)
	sendJSON(w, 200, map[string]any{"message": "Tersimpan", "profile": p})
}

// Upload file: icon_lencana / file_pdf / emoticon / aksesori_foto
func apiAdminEditProfileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	r.ParseMultipartForm(50 << 20) // 50 MB

	memberIDStr := r.FormValue("member_id")
	field := r.FormValue("field")
	memberID, err := strconv.Atoi(memberIDStr)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "member_id tidak valid"})
		return
	}
	subDir, ok := adminFileFields[field]
	if !ok {
		sendJSON(w, 400, map[string]string{"error": "Field upload tidak valid"})
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

	dir := filepath.Join(AdminUploadDir, subDir)
	os.MkdirAll(dir, 0755)

	dst, err := os.Create(filepath.Join(dir, saveName))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal simpan file"})
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	urlPath := "/uploads/profile/" + subDir + "/" + saveName

	DB.Exec("INSERT IGNORE INTO member_profile (member_id) VALUES (?)", memberID)
	col := field // nama kolom sama persis dengan nama field
	_, err = DB.Exec(fmt.Sprintf("UPDATE member_profile SET %s=? WHERE member_id=?", col), urlPath, memberID)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	p, _ := getAdminProfile(memberID)
	sendJSON(w, 201, map[string]any{"message": "Upload berhasil", "path": urlPath, "profile": p})
}

// ─────────────────────────────────────────
//  MENU "UPLOAD DATABASE" — 11 KATEGORI GLOBAL
//  File Pdf, Emot, Gambar, Pop Up, Text Berita,
//  Background Mimbar Bebas, Bingkai Foto Profil,
//  Lencana, Trofi, Musik, Bot
// ─────────────────────────────────────────

type AssetItem struct {
	No    int    `json:"no"`
	Key   string `json:"key"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

func apiAdminAssetsList(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query("SELECT category, file_path FROM site_assets")
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	paths := map[string]string{}
	for rows.Next() {
		var cat, path string
		rows.Scan(&cat, &path)
		paths[cat] = path
	}

	list := make([]AssetItem, 0, len(assetCategories))
	for i, c := range assetCategories {
		list = append(list, AssetItem{No: i + 1, Key: c.Key, Label: c.Label, Path: paths[c.Key]})
	}
	sendJSON(w, 200, list)
}

func apiAdminAssetsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	r.ParseMultipartForm(50 << 20) // 50 MB

	category := r.FormValue("category")
	if !assetCategoryKeys[category] {
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

	dir := filepath.Join(AdminUploadDir, "assets", category)
	os.MkdirAll(dir, 0755)

	dst, err := os.Create(filepath.Join(dir, saveName))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal simpan file"})
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	urlPath := "/uploads/profile/assets/" + category + "/" + saveName

	_, err = DB.Exec(`
		INSERT INTO site_assets (category, file_path) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE file_path = VALUES(file_path), updated_at = CURRENT_TIMESTAMP`,
		category, urlPath)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	sendJSON(w, 201, map[string]any{"message": "Kirim berhasil", "category": category, "path": urlPath})
}

// ─────────────────────────────────────────
//  HALAMAN INVENTARIS — daftar isi tabel `files` per kategori
//  (termasuk "foto_profil" yang otomatis terisi saat member mendaftar)
// ─────────────────────────────────────────

type InventarisItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Tanggal string `json:"tanggal"`
}

func apiAdminInventaris(w http.ResponseWriter, r *http.Request) {
	category := strings.TrimPrefix(r.URL.Path, "/api/inventaris/")
	if category == "" {
		sendJSON(w, 400, map[string]string{"error": "Kategori wajib diisi"})
		return
	}

	rows, err := DB.Query(
		"SELECT filename, path, uploaded_at FROM files WHERE category=? ORDER BY uploaded_at DESC LIMIT 10",
		category,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var list []InventarisItem
	for rows.Next() {
		var it InventarisItem
		var t time.Time
		if err := rows.Scan(&it.Name, &it.Path, &t); err != nil {
			continue
		}
		it.Tanggal = t.Format("2006-01-02 15:04:05")
		list = append(list, it)
	}
	if list == nil {
		list = []InventarisItem{}
	}
	sendJSON(w, 200, list)
}

// ── HTTP handler: aktifkan akun (ubah username temp → real, status → aktif) ──
func apiAdminAktifkanAkun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405); return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == 0 {
		sendJSON(w, 400, map[string]string{"error": "ID member tidak valid"}); return
	}
	profile, err := aktifkanAkun(body.ID)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()}); return
	}
	sendJSON(w, 200, map[string]any{
		"message":  "Akun berhasil diaktifkan",
		"username": profile.Username,
	})
}

// ── HTTP handler: suspend/cekal member ──
func apiAdminSuspendMember(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405); return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == 0 {
		sendJSON(w, 400, map[string]string{"error": "ID tidak valid"}); return
	}
	// Tandai akun di-suspend (status_akun = 'cekal') lewat member_profile → users
	var username string
	DB.QueryRow("SELECT COALESCE(username,'') FROM member_profile WHERE member_id=?", body.ID).Scan(&username)
	if username != "" {
		DB.Exec("UPDATE users SET status_akun='cekal' WHERE username=?", username)
	}
	// Juga set suspended = 1 di tabel members (untuk flag di panel lama)
	DB.Exec("UPDATE members SET suspended=1 WHERE id=?", body.ID)
	sendJSON(w, 200, map[string]string{"message": "Member berhasil di-suspend"})
}
