package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─────────────────────────────────────────
//  FOTO PROFIL (disimpan saat pendaftaran member)
// ─────────────────────────────────────────

var FotoProfilDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "uploads", "foto_profil")
}()

// ─────────────────────────────────────────
//  MODEL
// ─────────────────────────────────────────

type Member struct {
	ID               int      `json:"id"`
	Nama             string   `json:"nama"`
	Status           string   `json:"status"`
	StatusOverride   bool     `json:"status_override"`
	Lencana          []string `json:"lencana"`
	Trofi            []string `json:"trofi"`
	TanggalBergabung string   `json:"tanggal_bergabung"`
	KTA              string   `json:"kta"`
}

// ─────────────────────────────────────────
//  HELPER
// ─────────────────────────────────────────

func hitungStatus(tanggal string) string {
	tgl, err := time.Parse("2006-01-02", tanggal)
	if err != nil {
		return "Kohai"
	}
	hari := int(time.Since(tgl).Hours() / 24)
	switch {
	case hari < 30:
		return "Kohai"
	case hari < 90:
		return "Senpai"
	default:
		return "Dai Senpai"
	}
}

func generateKTA(tanggal string) string {
	tglFmt := strings.ReplaceAll(tanggal, "-", "")
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM members WHERE tanggal_bergabung = ?", tanggal).Scan(&count)
	return fmt.Sprintf("KTA-%s-%02d", tglFmt, count+1)
}

var nonUsernameChars = regexp.MustCompile(`[^a-z0-9]+`)

// Validasi pendaftaran: email format umum, password min 4 karakter dengan
// minimal 1 angka dan 1 huruf besar (dicek juga di sisi client daftar.html).
var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
var hasDigit = regexp.MustCompile(`[0-9]`)
var hasUpper = regexp.MustCompile(`[A-Z]`)

// generateUsername membuat username dari nama (huruf kecil, tanpa spasi/simbol),
// lalu memastikan unik di tabel users (tambah angka di belakang kalau sudah dipakai).
// Dipakai saat admin MENGAKTIFKAN akun (lihat admin_edit.go), bukan saat daftar.
func generateUsername(nama string) string {
	base := nonUsernameChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(nama)), "")
	if base == "" {
		base = "member"
	}
	username := base
	var exists int
	for i := 0; ; i++ {
		if i > 0 {
			username = fmt.Sprintf("%s%d", base, i)
		}
		DB.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&exists)
		if exists == 0 {
			return username
		}
	}
}

// generateTempUsername membuat identitas sementara berbasis waktu daftar,
// misal "01:57-26-07-2026", dipakai selama akun masih berstatus 'pending'
// (belum dikonfirmasi admin lewat panel "My Lord").
func generateTempUsername() string {
	base := time.Now().Format("15:04-02-01-2006")
	username := base
	var exists int
	for i := 2; ; i++ {
		DB.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&exists)
		if exists == 0 {
			return username
		}
		username = fmt.Sprintf("%s-%d", base, i)
	}
}

var unsafeFileChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// safeFileName menyiapkan nama file aman dari karakter yang tidak didukung
// filesystem (mis. ":" pada identitas sementara waktu daftar).
func safeFileName(s string) string {
	return unsafeFileChars.ReplaceAllString(s, "-")
}

// simpanFotoProfil menyimpan file foto dengan nama = username, lalu mencatatnya
// di tabel files (kategori "foto_profil") supaya muncul di halaman Inventaris admin
func simpanFotoProfil(username string, file multipart.File, header *multipart.FileHeader) (string, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	saveName := safeFileName(username) + ext

	if err := os.MkdirAll(FotoProfilDir, 0755); err != nil {
		return "", err
	}
	dst, err := os.Create(filepath.Join(FotoProfilDir, saveName))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}

	urlPath := "/uploads/foto_profil/" + saveName
	_, err = DB.Exec(
		"INSERT INTO files (filename, category, path) VALUES (?,?,?)",
		username, "foto_profil", urlPath,
	)
	return urlPath, err
}

func scanMember(rows interface{ Scan(...any) error }) (*Member, error) {
	var m Member
	var lencanaJSON, trofiJSON []byte
	var override int
	err := rows.Scan(&m.ID, &m.Nama, &m.Status, &override, &lencanaJSON, &trofiJSON, &m.TanggalBergabung, &m.KTA)
	if err != nil {
		return nil, err
	}
	m.StatusOverride = override == 1
	if !m.StatusOverride {
		m.Status = hitungStatus(m.TanggalBergabung)
	}
	if lencanaJSON != nil {
		json.Unmarshal(lencanaJSON, &m.Lencana)
	}
	if m.Lencana == nil {
		m.Lencana = []string{}
	}
	if trofiJSON != nil {
		json.Unmarshal(trofiJSON, &m.Trofi)
	}
	if m.Trofi == nil {
		m.Trofi = []string{}
	}
	return &m, nil
}

func getMemberByID(id string) (*Member, error) {
	row := DB.QueryRow("SELECT id,nama,status,status_override,lencana,trofi,tanggal_bergabung,kta FROM members WHERE id=?", id)
	return scanMember(row)
}

func getAllMembers() ([]*Member, error) {
	rows, err := DB.Query("SELECT id,nama,status,status_override,lencana,trofi,tanggal_bergabung,kta FROM members ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err == nil {
			list = append(list, m)
		}
	}
	if list == nil {
		list = []*Member{}
	}
	return list, nil
}

func serveHTML(w http.ResponseWriter, filename string) {
	path := filepath.Join(BaseDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, filename+" tidak ditemukan", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func sendJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ─────────────────────────────────────────
//  ROUTES
// ─────────────────────────────────────────

func RegisterMemberRoutes(mux *http.ServeMux) {
	os.MkdirAll(FotoProfilDir, 0755)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "daftar.html")
	})
	mux.HandleFunc("/daftar", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "daftar.html")
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "admin.html")
	})
	mux.Handle("/uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(filepath.Join(FotoProfilDir, "..")))))

	// API
	mux.HandleFunc("/api/members", apiMembers)
	mux.HandleFunc("/api/member/", apiMemberByID)
	mux.HandleFunc("/api/daftar", apiDaftar)
	mux.HandleFunc("/api/cek-email", apiCekEmail)
	mux.HandleFunc("/api/admin/status", apiAdminStatus)
	mux.HandleFunc("/api/admin/status/reset", apiAdminStatusReset)
	mux.HandleFunc("/api/admin/lencana", apiAdminLencana)
	mux.HandleFunc("/api/admin/trofi", apiAdminTrofi)
}

// ─────────────────────────────────────────
//  CEK EMAIL (validasi real-time, tanpa kirim email)
//
//  PENTING: Google/Gmail TIDAK menyediakan cara untuk mengecek dari luar
//  apakah satu alamat email spesifik (mis. budi123@gmail.com) benar-benar
//  ada dan aktif digunakan. Ini sengaja dibatasi Google untuk mencegah
//  penyalahgunaan (email enumeration untuk spam/phishing).
//
//  Strategi:
//  1) Domain penyedia email besar & sudah pasti aktif (gmail.com, yahoo.com,
//     outlook.com, dst) -> langsung dianggap valid tanpa perlu cek jaringan
//     apapun. Ini menghindari false-negative akibat DNS lookup gagal di
//     lingkungan seperti Termux/Android yang koneksinya kadang terbatas.
//  2) Domain di luar daftar itu -> baru dicoba DNS MX lookup sebagai
//     pengecekan tambahan.
//  3) Jika langkah DNS lookup itu sendiri GAGAL karena masalah jaringan
//     (bukan karena domainnya benar-benar tidak ada), status dikembalikan
//     "unknown" -- BUKAN "tidak aktif". Peringatan merah hanya muncul untuk
//     domain yang benar-benar terbukti tidak bisa menerima email.
// ─────────────────────────────────────────

var knownEmailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true,
	"yahoo.com": true, "yahoo.co.id": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true,
	"icloud.com": true, "me.com": true,
	"proton.me": true, "protonmail.com": true,
	"aol.com": true, "zoho.com": true,
}

func apiCekEmail(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))

	if email == "" || !emailRegex.MatchString(email) {
		sendJSON(w, 200, map[string]interface{}{
			"valid":  false,
			"reason": "format",
		})
		return
	}

	parts := strings.Split(email, "@")
	domain := strings.ToLower(parts[len(parts)-1])

	// Domain penyedia besar yang sudah pasti aktif -> valid langsung
	if knownEmailDomains[domain] {
		sendJSON(w, 200, map[string]interface{}{
			"valid":  true,
			"domain": domain,
		})
		return
	}

	// Domain lain -> coba DNS MX lookup sebagai pengecekan tambahan
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		// Gagal lookup BISA berarti domain memang tidak ada, TAPI juga bisa
		// karena masalah jaringan di server (DNS resolver tidak terjangkau).
		// Untuk menghindari salah menandai email valid sebagai "tidak aktif",
		// status dikembalikan "unknown" dan tidak dianggap error.
		sendJSON(w, 200, map[string]interface{}{
			"valid":  false,
			"reason": "unknown",
		})
		return
	}
	if len(mxRecords) == 0 {
		sendJSON(w, 200, map[string]interface{}{
			"valid":  false,
			"reason": "domain",
		})
		return
	}

	sendJSON(w, 200, map[string]interface{}{
		"valid":  true,
		"domain": domain,
	})
}

func apiMembers(w http.ResponseWriter, r *http.Request) {
	list, err := getAllMembers()
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	sendJSON(w, 200, list)
}

func apiMemberByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/member/")
	m, err := getMemberByID(id)
	if err != nil {
		sendJSON(w, 404, map[string]string{"error": "tidak ditemukan"})
		return
	}
	sendJSON(w, 200, m)
}

func apiDaftar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405); return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB
		sendJSON(w, 400, map[string]string{"error": "Data form tidak valid"}); return
	}

	nama := strings.TrimSpace(r.FormValue("nama"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if nama == "" {
		sendJSON(w, 400, map[string]string{"error": "Nama wajib diisi"}); return
	}
	if email == "" {
		sendJSON(w, 400, map[string]string{"error": "Email aktif wajib diisi"}); return
	}
	if !emailRegex.MatchString(email) {
		sendJSON(w, 400, map[string]string{"error": "Format email tidak valid"}); return
	}
	if password == "" {
		sendJSON(w, 400, map[string]string{"error": "Password wajib diisi"}); return
	}
	if len(password) < 4 || !hasDigit.MatchString(password) || !hasUpper.MatchString(password) {
		sendJSON(w, 400, map[string]string{"error": "Password harus minimal 4 karakter, 1 angka & 1 huruf besar"}); return
	}

	file, header, err := r.FormFile("foto")
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "Pas foto wajib diunggah"}); return
	}
	defer file.Close()

	// 1) Buat akun login dengan identitas SEMENTARA berbasis waktu daftar
	//    (mis. "01:57-26-07-2026"), status_akun = 'pending'. Identitas ini
	//    baru diganti jadi username asli oleh admin lewat panel "My Lord"
	//    setelah dikonfirmasi dan akun diaktifkan (lihat admin_edit.go).
	username := generateTempUsername()
	_, err = DB.Exec(
		"INSERT INTO users (username, password, email, role, scope, status_akun) VALUES (?, SHA2(?,256), ?, 'member', 'member', 'pending')",
		username, password, email,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal membuat akun: " + err.Error()}); return
	}

	// 2) Simpan foto profil ke folder server, nama file = username,
	//    dan catat di tabel files (kategori foto_profil) supaya otomatis
	//    tampil di halaman Inventaris panel Admin Edit "My Lord" (port 8084).
	//    Jika gagal, akun yang sudah terlanjur dibuat di langkah 1 dihapus
	//    lagi supaya data nama/foto/password tetap konsisten.
	if _, err := simpanFotoProfil(username, file, header); err != nil {
		DB.Exec("DELETE FROM users WHERE username=?", username)
		sendJSON(w, 500, map[string]string{"error": "Gagal menyimpan foto: " + err.Error()}); return
	}

	// 3) Buat data member
	today := time.Now().Format("2006-01-02")
	kta := generateKTA(today)
	_, err = DB.Exec(
		"INSERT INTO members (nama,status,status_override,lencana,trofi,tanggal_bergabung,kta) VALUES (?,?,0,'[]','[]',?,?)",
		nama, "Kohai", today, kta,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()}); return
	}
	var id int
	DB.QueryRow("SELECT id FROM members WHERE kta=?", kta).Scan(&id)

	// 4) Simpan username juga ke member_profile supaya tampil di panel admin "My Lord"
	DB.Exec("INSERT IGNORE INTO member_profile (member_id) VALUES (?)", id)
	DB.Exec("UPDATE member_profile SET username=? WHERE member_id=?", username, id)

	m, _ := getMemberByID(fmt.Sprint(id))
	sendJSON(w, 201, map[string]any{"message": "Berhasil didaftarkan!", "member": m, "username": username})
}

func apiAdminStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	var body struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	readJSON(r, &body)
	valid := map[string]bool{"Kohai": true, "Senpai": true, "Dai Senpai": true}
	if !valid[body.Status] {
		sendJSON(w, 400, map[string]string{"error": "Status tidak valid"}); return
	}
	DB.Exec("UPDATE members SET status=?, status_override=1 WHERE id=?", body.Status, body.ID)
	m, _ := getMemberByID(fmt.Sprint(body.ID))
	sendJSON(w, 200, map[string]any{"message": "Status diperbarui", "member": m})
}

func apiAdminStatusReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	var body struct{ ID int `json:"id"` }
	readJSON(r, &body)
	DB.Exec("UPDATE members SET status_override=0 WHERE id=?", body.ID)
	m, _ := getMemberByID(fmt.Sprint(body.ID))
	sendJSON(w, 200, map[string]any{"message": "Status otomatis", "member": m})
}

func apiAdminLencana(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	var body struct {
		ID      int    `json:"id"`
		Lencana string `json:"lencana"`
	}
	readJSON(r, &body)
	if strings.TrimSpace(body.Lencana) == "" {
		sendJSON(w, 400, map[string]string{"error": "Nama lencana wajib"}); return
	}
	var lencanaJSON []byte
	DB.QueryRow("SELECT lencana FROM members WHERE id=?", body.ID).Scan(&lencanaJSON)
	var list []string
	json.Unmarshal(lencanaJSON, &list)
	// Cek duplikat
	for _, l := range list {
		if l == body.Lencana { break }
	}
	list = append(list, body.Lencana)
	newJSON, _ := json.Marshal(list)
	DB.Exec("UPDATE members SET lencana=? WHERE id=?", string(newJSON), body.ID)
	m, _ := getMemberByID(fmt.Sprint(body.ID))
	sendJSON(w, 200, map[string]any{"message": "Lencana ditambahkan", "member": m})
}

func apiAdminTrofi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	var body struct {
		ID    int    `json:"id"`
		Trofi string `json:"trofi"`
	}
	readJSON(r, &body)
	valid := map[string]bool{"Emas": true, "Perak": true, "Perunggu": true}
	if !valid[body.Trofi] {
		sendJSON(w, 400, map[string]string{"error": "Trofi harus Emas/Perak/Perunggu"}); return
	}
	var trofiJSON []byte
	DB.QueryRow("SELECT trofi FROM members WHERE id=?", body.ID).Scan(&trofiJSON)
	var list []string
	json.Unmarshal(trofiJSON, &list)
	list = append(list, body.Trofi)
	newJSON, _ := json.Marshal(list)
	DB.Exec("UPDATE members SET trofi=? WHERE id=?", string(newJSON), body.ID)
	m, _ := getMemberByID(fmt.Sprint(body.ID))
	sendJSON(w, 200, map[string]any{"message": "Trofi ditambahkan", "member": m})
}
