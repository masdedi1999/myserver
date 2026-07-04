package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────
//  ELEARNING CONFIG
// ─────────────────────────────────────────

var AudioDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "audio")
}()

var ChatImageDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "elearning_chat_images")
}()

//go:embed web/elearning_home.html
var elearningHomeHTML string

const elPageStyle = `
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:sans-serif;background:#0d1117;color:#e6edf3;padding:16px;padding-bottom:60px}
h1{font-size:20px;margin-bottom:16px;color:#58a6ff}
h2{font-size:15px;margin:20px 0 10px;color:#8b949e;text-transform:uppercase;letter-spacing:1px}
.nav{display:flex;gap:12px;margin-bottom:20px;flex-wrap:wrap}
.nav a{color:#58a6ff;text-decoration:none;font-size:14px}
.card{background:#161b22;border:1px solid #21262d;border-radius:12px;
padding:16px;margin-bottom:10px}
.card:active{border-color:#58a6ff}
.cat-link{display:block;color:#e6edf3;text-decoration:none}
.cat-name{font-size:15px;font-weight:600}
.cat-meta{font-size:12px;color:#8b949e;margin-top:4px}
input,select,textarea{background:#0d1117;border:1px solid #30363d;color:#e6edf3;
padding:9px 12px;border-radius:8px;font-size:13px;margin-bottom:8px;width:100%;display:block}
button{background:#1f6feb;color:#fff;border:none;padding:10px 18px;
border-radius:8px;font-size:14px;cursor:pointer}
.form-box{background:#161b22;border:1px solid #21262d;border-radius:12px;padding:16px;margin-bottom:16px}
table{width:100%;border-collapse:collapse;background:#161b22;border-radius:12px;overflow:hidden;margin-bottom:8px}
th{background:#21262d;padding:8px;text-align:left;font-size:12px;color:#8b949e}
td{padding:8px;border-top:1px solid #21262d;font-size:13px}
a.del{color:#f85149;text-decoration:none}
.qcard{background:#161b22;border:1px solid #21262d;border-radius:16px;padding:20px;text-align:center;max-width:420px;margin:20px auto}
.qcard audio{width:100%;margin-bottom:16px}
.qcard .pertanyaan{font-size:16px;margin-bottom:18px;color:#e6edf3}
.opt{display:block;width:100%;text-align:left;background:#0d1117;border:1px solid #30363d;
color:#e6edf3;padding:12px 14px;border-radius:10px;margin-bottom:10px;font-size:14px;cursor:pointer}
.opt:active{border-color:#58a6ff}
.opt.correct{background:#0d2818;border-color:#3fb950;color:#3fb950}
.opt.wrong{background:#2b0d0d;border-color:#f85149;color:#f85149}
.progress{font-size:13px;color:#8b949e;margin-bottom:10px}
.empty{text-align:center;color:#8b949e;padding:40px 20px}
`

// ─────────────────────────────────────────
//  HELPER — SESSION / USER
// ─────────────────────────────────────────

func elCurrentUser(r *http.Request) (id int, username, role string, ok bool) {
	c, err := r.Cookie("el_token")
	if err != nil {
		return 0, "", "", false
	}
	err = DB.QueryRow(`SELECT u.id,u.username,u.role FROM sessions_elearning s
		JOIN users u ON s.user_id=u.id WHERE s.token=? AND s.expires_at>?`,
		c.Value, time.Now().Unix()).Scan(&id, &username, &role)
	if err != nil {
		return 0, "", "", false
	}
	return id, username, role, true
}

func elRequireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, _, ok := elCurrentUser(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func elRequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, role, ok := elCurrentUser(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if role != "admin" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(403)
			w.Write([]byte(`<h1 style="color:#f85149;font-family:sans-serif;padding:20px">403 - Akses Ditolak</h1>`))
			return
		}
		next(w, r)
	}
}

func elJSSafe(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// ─────────────────────────────────────────
//  ROUTES
// ─────────────────────────────────────────

func RegisterElearningRoutes(mux *http.ServeMux) {
	os.MkdirAll(AudioDir, 0755)
	os.MkdirAll(ChatImageDir, 0755)

	// Halaman utama: Mimbar Bebas (chat + liga + lencana + organisasi)
	mux.HandleFunc("/", elRequireLogin(elHomeHandler))
	mux.HandleFunc("/login", elLoginHandler)
	mux.HandleFunc("/lupa-password", elLupaPasswordHandler)
	mux.HandleFunc("/logout", elLogoutHandler)

	// Chat backend
	mux.HandleFunc("/chat/messages", elRequireLogin(elChatMessages))
	mux.HandleFunc("/chat/send", elRequireLogin(elChatSend))
	mux.HandleFunc("/chat/image", elRequireLogin(elChatImage))

	// Profil "My Personality" milik user yang sedang login
	mux.HandleFunc("/api/me/profile", elRequireLogin(apiElMyProfile))
	mux.HandleFunc("/api/user/profile", elRequireLogin(apiElUserProfile))

	// Statistik La Liga (menang/kalah per liga per tier, per user)
	mux.HandleFunc("/api/liga/stats", elRequireLogin(apiElLigaStats))
	mux.HandleFunc("/api/liga/hasil", elRequireLogin(apiElLigaHasil))

	// Materi / kuis (kategori + soal audio)
	mux.HandleFunc("/elearning", elRequireLogin(elCategoryListHandler))
	mux.HandleFunc("/elearning/kategori", elRequireLogin(elQuizPageHandler))
	mux.HandleFunc("/elearning/audio", elRequireLogin(elAudioHandler))

	// Admin kelola materi
	mux.HandleFunc("/elearning/admin", elRequireAdmin(elAdminPageHandler))
	mux.HandleFunc("/elearning/admin/tambahkategori", elRequireAdmin(elAdminAddCategory))
	mux.HandleFunc("/elearning/admin/delkategori", elRequireAdmin(elAdminDelCategory))
	mux.HandleFunc("/elearning/admin/tambahsoal", elRequireAdmin(elAdminAddQuestion))
	mux.HandleFunc("/elearning/admin/delsoal", elRequireAdmin(elAdminDelQuestion))

	mux.Handle("/audio/", http.StripPrefix("/audio/",
		http.FileServer(http.Dir(AudioDir))))

	// JSON API lama (tetap dipertahankan untuk kompatibilitas)
	mux.HandleFunc("/api/login", apiElearningLogin)
	mux.HandleFunc("/api/logout", apiElearningLogout)
	mux.HandleFunc("/api/categories", authElearningMiddleware(apiELCategories))
	mux.HandleFunc("/api/category/add", authElearningMiddleware(apiELCategoryAdd))
	mux.HandleFunc("/api/category/delete", authElearningMiddleware(apiELCategoryDelete))
	mux.HandleFunc("/api/questions", authElearningMiddleware(apiELQuestions))
	mux.HandleFunc("/api/question/add", authElearningMiddleware(apiELQuestionAdd))
	mux.HandleFunc("/api/question/delete", authElearningMiddleware(apiELQuestionDelete))
	mux.HandleFunc("/api/question/audio", authElearningMiddleware(apiELQuestionAudio))
	mux.HandleFunc("/api/quiz", authElearningMiddleware(apiELQuiz))
	mux.HandleFunc("/api/submit", authElearningMiddleware(apiELSubmit))
}

// ─────────────────────────────────────────
//  MIDDLEWARE AUTH LAMA (dipakai /api/*)
// ─────────────────────────────────────────

func authElearningMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Token")
		if token == "" {
			cookie, err := r.Cookie("el_token")
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			token = cookie.Value
		}
		var userID int
		var expires int64
		err := DB.QueryRow(
			"SELECT user_id, expires_at FROM sessions_elearning WHERE token=?", token,
		).Scan(&userID, &expires)
		if err != nil || time.Now().Unix() > expires {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// ─────────────────────────────────────────
//  HALAMAN UTAMA — MIMBAR BEBAS
// ─────────────────────────────────────────

func elHomeHandler(w http.ResponseWriter, r *http.Request) {
	_, username, _, _ := elCurrentUser(r)
	if username == "" {
		username = "Tamu"
	}
	out := strings.Replace(elearningHomeHTML,
		"const ME = '__USERNAME__';",
		"const ME = '"+elJSSafe(username)+"';", 1)
	out = strings.ReplaceAll(out, "__USERNAME__", html.EscapeString(username))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

// ─────────────────────────────────────────
//  LOGIN / LOGOUT (halaman, bukan JSON)
// ─────────────────────────────────────────

func elLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		username := r.FormValue("username")
		password := r.FormValue("password")
		var userID int
		var statusAkun string
		err := DB.QueryRow(
			`SELECT id, COALESCE(status_akun,'aktif') FROM users WHERE username=? AND password=SHA2(?,256)`,
			username, password,
		).Scan(&userID, &statusAkun)
		if err != nil {
			http.Redirect(w, r, "/login?error=1", http.StatusFound)
			return
		}
		if statusAkun == "pending" {
			http.Redirect(w, r, "/login?error=pending", http.StatusFound)
			return
		}
		if statusAkun == "cekal" {
			http.Redirect(w, r, "/login?error=cekal", http.StatusFound)
			return
		}
		token := generateToken()
		expires := time.Now().Add(7 * 24 * time.Hour).Unix()
		DB.Exec("INSERT INTO sessions_elearning (token, user_id, expires_at) VALUES (?,?,?)",
			token, userID, expires)
		http.SetCookie(w, &http.Cookie{
			Name:    "el_token",
			Value:   token,
			Path:    "/",
			Expires: time.Now().Add(7 * 24 * time.Hour),
		})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	errParam := r.URL.Query().Get("error")
	errHTML := ""
	switch errParam {
	case "1":
		errHTML = `<div class="err">Username/password salah.</div>`
	case "pending":
		errHTML = `<div class="err">Akun kamu sedang menunggu konfirmasi admin. Mohon bersabar.</div>`
	case "cekal":
		errHTML = `<div class="err">Akun kamu telah dinonaktifkan. Hubungi admin.</div>`
	}
	out := fmt.Sprintf(`<!DOCTYPE html>
<html lang="id"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>Login</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:sans-serif;background:#0d1117;min-height:100vh;display:flex;
flex-direction:column;align-items:center;justify-content:center;padding:16px}
.box{background:#161b22;border:1px solid #21262d;border-radius:16px;
padding:32px 24px;width:100%%;max-width:320px;text-align:center}
h1{font-size:20px;color:#e6edf3;margin:0 0 20px;letter-spacing:1px}
input{width:100%%;padding:11px 14px;margin-bottom:12px;border:1px solid #30363d;
border-radius:8px;font-size:14px;background:#0d1117;color:#e6edf3;outline:none;display:block}
input:focus{border-color:#1f6feb}
button{width:100%%;padding:12px;background:#1f6feb;color:#fff;border:none;
border-radius:8px;font-size:15px;font-weight:700;cursor:pointer}
.err{background:#2b0d0d;color:#f85149;border:1px solid #4b1a1a;
padding:9px 12px;border-radius:8px;font-size:13px;margin-bottom:14px;text-align:left}
.login-links{margin-top:16px;display:flex;flex-direction:column;gap:8px;}
.login-links a{color:#58a6ff;font-size:13px;text-decoration:none;}
.login-links a:hover{text-decoration:underline;}
</style></head><body>
<div class="box">
  <h1>🎓 My E-Learning</h1>
  %s
  <form method="POST" action="/login">
    <input type="text" name="username" placeholder="Username" required>
    <input type="password" name="password" placeholder="Password" required>
    <button type="submit">Masuk</button>
  </form>
  <div class="login-links">
    <a href="/daftar">Belum punya akun? Daftar di sini</a>
    <a href="/lupa-password">Lupa kata sandi?</a>
  </div>
</div>
</body></html>`, errHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

// elLupaPasswordHandler menampilkan halaman informasi untuk akun yang lupa
// kata sandi. Sistem ini belum punya reset password otomatis (butuh email
// terverifikasi/SMTP), jadi user diarahkan menghubungi admin secara manual
// melalui panel Admin Edit "My Lord" (port 8084), konsisten dengan alur
// akun cekal/pending yang juga diarahkan menghubungi admin.
func elLupaPasswordHandler(w http.ResponseWriter, r *http.Request) {
	out := `<!DOCTYPE html>
<html lang="id"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>Lupa Kata Sandi</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:sans-serif;background:#0d1117;min-height:100vh;display:flex;
flex-direction:column;align-items:center;justify-content:center;padding:16px}
.box{background:#161b22;border:1px solid #21262d;border-radius:16px;
padding:32px 24px;width:100%;max-width:340px;text-align:center}
h1{font-size:18px;color:#e6edf3;margin:0 0 14px}
p{color:#8b949e;font-size:13.5px;line-height:1.6;margin-bottom:20px}
a.back{display:inline-block;width:100%;padding:12px;background:#1f6feb;color:#fff;
text-decoration:none;border:none;border-radius:8px;font-size:14px;font-weight:700}
a.back:hover{background:#388bfd}
</style></head><body>
<div class="box">
  <h1>🔒 Lupa Kata Sandi</h1>
  <p>Sistem reset kata sandi otomatis belum tersedia. Silakan hubungi admin untuk mengatur ulang kata sandi akunmu.</p>
  <a class="back" href="/login">Kembali ke Login</a>
</div>
</body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func elLogoutHandler(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("el_token")
	if err == nil {
		DB.Exec("DELETE FROM sessions_elearning WHERE token=?", c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "el_token", Value: "", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ─────────────────────────────────────────
//  CHAT (publik, dipakai halaman Mimbar Bebas)
// ─────────────────────────────────────────

type chatMsgOut struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	FileType  string `json:"file_type"`
	FileName  string `json:"file_name"`
}

func elChatMessages(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	rows, err := DB.Query(`SELECT id, username, message, created_at, image_path
		FROM chat_messages WHERE id>? AND deleted=0 ORDER BY id ASC LIMIT 50`, since)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var msgs []chatMsgOut
	for rows.Next() {
		var id int
		var username, message string
		var createdAt time.Time
		var imagePath *string
		if err := rows.Scan(&id, &username, &message, &createdAt, &imagePath); err != nil {
			continue
		}
		m := chatMsgOut{ID: id, Username: username, Message: message, CreatedAt: createdAt.Format("2006-01-02 15:04:05")}
		if imagePath != nil && *imagePath != "" {
			ext := strings.ToLower(filepath.Ext(*imagePath))
			imgExt := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true, ".heif": true, ".bmp": true}
			fname := filepath.Base(*imagePath)
			if idx := strings.Index(fname, "_"); idx != -1 {
				fname = fname[idx+1:]
			}
			if imgExt[ext] {
				m.FileType = "image"
			} else {
				m.FileType = "file"
			}
			m.FileName = fname
		}
		msgs = append(msgs, m)
	}
	if msgs == nil {
		msgs = []chatMsgOut{}
	}
	sendJSON(w, 200, map[string]any{"messages": msgs})
}

func elChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	uid, uname, _, ok := elCurrentUser(r)
	if !ok {
		sendJSON(w, 401, map[string]bool{"ok": false})
		return
	}

	var message string
	var imagePath string

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err == nil {
			message = strings.TrimSpace(r.FormValue("message"))
			if len(message) > 300 {
				message = message[:300]
			}
			file, header, ferr := r.FormFile("image")
			if ferr == nil {
				defer file.Close()
				safeName := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filepath.Base(header.Filename))
				dest := filepath.Join(ChatImageDir, safeName)
				out, derr := os.Create(dest)
				if derr == nil {
					defer out.Close()
					io.Copy(out, file)
					imagePath = dest
				}
			}
		}
	} else {
		r.ParseForm()
		message = strings.TrimSpace(r.FormValue("message"))
		if len(message) > 300 {
			message = message[:300]
		}
	}

	if message == "" && imagePath == "" {
		sendJSON(w, 200, map[string]bool{"ok": false})
		return
	}

	var imgArg any
	if imagePath != "" {
		imgArg = imagePath
	} else {
		imgArg = nil
	}
	DB.Exec("INSERT INTO chat_messages (user_id,username,message,image_path) VALUES (?,?,?,?)",
		uid, uname, message, imgArg)
	sendJSON(w, 200, map[string]bool{"ok": true})
}

func elChatImage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var imagePath *string
	err := DB.QueryRow("SELECT image_path FROM chat_messages WHERE id=? AND deleted=0", id).Scan(&imagePath)
	if err != nil || imagePath == nil || *imagePath == "" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(*imagePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fname := filepath.Base(*imagePath)
	if idx := strings.Index(fname, "_"); idx != -1 {
		fname = fname[idx+1:]
	}
	mt := mime.TypeByExtension(filepath.Ext(*imagePath))
	if mt == "" {
		mt = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, fname))
	w.Write(data)
}

// ─────────────────────────────────────────
//  API: PROFIL "MY PERSONALITY" (user yang sedang login)
//  Menggabungkan data members + member_profile milik user login,
//  supaya panel My Personality di elearning_home.html tidak lagi
//  hardcoded dan otomatis berbeda antar akun.
// ─────────────────────────────────────────

// Daftar liga & tier yang valid (whitelist, dipakai juga untuk validasi input)
var daftarLiga = map[string]bool{
	"Liga Minna 1": true, "Liga Minna 2": true, "Liga N5": true,
	"Liga N4": true, "Liga N3": true, "Liga Choukai": true, "Liga Kanji": true,
}
var daftarTier = map[string]bool{"emas": true, "perak": true, "perunggu": true}

type MyProfileOut struct {
	Username         string         `json:"username"`
	KTA              string         `json:"kta"`
	Status           string         `json:"status"`
	StatusOverride   bool           `json:"status_override"`
	Lencana          []string       `json:"lencana"`
	TrofiCount       map[string]int `json:"trofi_count"` // {"emas":N,"perak":N,"perunggu":N} — jumlah liga dgn rekor di tier itu
	TanggalBergabung string         `json:"tanggal_bergabung"` // format "2006-01-02" atau "" jika belum ada
	FotoProfil       string         `json:"foto_profil"`
	HasMember        bool           `json:"has_member"` // false jika akun ini belum tertaut ke data member manapun
}

// hitungTrofiDariLigaStats menghitung jumlah LIGA (bukan jumlah baris) yang
// punya rekor menang>0 di masing-masing tier untuk seorang member. Satu liga
// dihitung sebagai 1 trofi per tier di mana member itu punya kemenangan.
func hitungTrofiDariLigaStats(memberID int) map[string]int {
	count := map[string]int{"emas": 0, "perak": 0, "perunggu": 0}
	rows, err := DB.Query(
		"SELECT tier, COUNT(*) FROM liga_stats WHERE member_id=? AND menang>0 GROUP BY tier",
		memberID,
	)
	if err != nil {
		return count
	}
	defer rows.Close()
	for rows.Next() {
		var tier string
		var n int
		if rows.Scan(&tier, &n) == nil {
			count[tier] = n
		}
	}
	return count
}

func apiElMyProfile(w http.ResponseWriter, r *http.Request) {
	_, username, _, ok := elCurrentUser(r)
	if !ok {
		sendJSON(w, 401, map[string]string{"error": "Belum login"})
		return
	}
	sendJSON(w, 200, buildProfileOut(username))
}

// apiElUserProfile mengembalikan profil publik dari username manapun,
// dipakai untuk popup profil saat long-press nama di Mimbar Bebas.
func apiElUserProfile(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := elCurrentUser(r)
	if !ok {
		sendJSON(w, 401, map[string]string{"error": "Belum login"})
		return
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		sendJSON(w, 400, map[string]string{"error": "username wajib diisi"})
		return
	}
	sendJSON(w, 200, buildProfileOut(username))
}

// buildProfileOut menyusun data profil (KTA, status, lencana, trofi, dsb)
// untuk sebuah username. Dipakai bersama oleh apiElMyProfile dan apiElUserProfile.
func buildProfileOut(username string) MyProfileOut {
	out := MyProfileOut{
		Username:   username,
		Lencana:    []string{},
		TrofiCount: map[string]int{"emas": 0, "perak": 0, "perunggu": 0},
	}

	var memberID int
	var kta, status, tanggal string
	var override int
	var lencanaJSON []byte

	err := DB.QueryRow(`
		SELECT m.id, m.kta, m.status, m.status_override, m.lencana, m.tanggal_bergabung
		FROM member_profile mp
		JOIN members m ON m.id = mp.member_id
		WHERE mp.username = ?`, username,
	).Scan(&memberID, &kta, &status, &override, &lencanaJSON, &tanggal)

	if err != nil {
		// Akun ini belum tertaut ke data member (mis. akun admin default).
		// Kembalikan papan kosong, bukan error, supaya frontend tetap bisa render.
		return out
	}

	out.HasMember = true
	out.KTA = kta
	out.StatusOverride = override == 1
	if out.StatusOverride {
		out.Status = status
	} else {
		out.Status = hitungStatus(tanggal)
	}
	out.TanggalBergabung = tanggal

	if lencanaJSON != nil {
		var l []string
		if json.Unmarshal(lencanaJSON, &l) == nil {
			out.Lencana = l
		}
	}

	out.TrofiCount = hitungTrofiDariLigaStats(memberID)

	var foto string
	DB.QueryRow(`
		SELECT path FROM files WHERE category='foto_profil' AND filename=?
		ORDER BY id DESC LIMIT 1`, username,
	).Scan(&foto)
	out.FotoProfil = foto

	return out
}

// getMemberIDByUsername mencari member_id yang tertaut ke sebuah akun login.
func getMemberIDByUsername(username string) (int, error) {
	var id int
	err := DB.QueryRow("SELECT member_id FROM member_profile WHERE username=?", username).Scan(&id)
	return id, err
}

// ─────────────────────────────────────────
//  API: STATISTIK LA LIGA (menang/kalah per liga per tier)
// ─────────────────────────────────────────

type LigaTierStat struct {
	Menang int `json:"menang"`
	Kalah  int `json:"kalah"`
}

// GET /api/liga/stats — rekap lengkap semua liga milik user yang login,
// dipakai untuk mengisi halaman detail trofi (per liga, per tier) secara dinamis.
func apiElLigaStats(w http.ResponseWriter, r *http.Request) {
	_, username, _, ok := elCurrentUser(r)
	if !ok {
		sendJSON(w, 401, map[string]string{"error": "Belum login"})
		return
	}
	memberID, err := getMemberIDByUsername(username)
	if err != nil {
		sendJSON(w, 200, map[string]any{}) // belum tertaut ke member, balikin kosong
		return
	}

	rows, err := DB.Query("SELECT liga, tier, menang, kalah FROM liga_stats WHERE member_id=?", memberID)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	// out[liga][tier] = {menang, kalah}
	out := map[string]map[string]LigaTierStat{}
	for rows.Next() {
		var liga, tier string
		var st LigaTierStat
		if rows.Scan(&liga, &tier, &st.Menang, &st.Kalah) != nil {
			continue
		}
		if out[liga] == nil {
			out[liga] = map[string]LigaTierStat{}
		}
		out[liga][tier] = st
	}
	sendJSON(w, 200, out)
}

// POST /api/liga/hasil — dipanggil setelah satu sesi liga selesai dikerjakan.
// Body: {"liga":"Liga N4","tier":"emas","menang":true}
// Menambah 1 ke kolom menang atau kalah (upsert) untuk member yang sedang login.
func apiElLigaHasil(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	_, username, _, ok := elCurrentUser(r)
	if !ok {
		sendJSON(w, 401, map[string]string{"error": "Belum login"})
		return
	}
	var body struct {
		Liga   string `json:"liga"`
		Tier   string `json:"tier"`
		Menang bool   `json:"menang"`
	}
	if err := readJSON(r, &body); err != nil {
		sendJSON(w, 400, map[string]string{"error": "Body tidak valid"})
		return
	}
	if !daftarLiga[body.Liga] {
		sendJSON(w, 400, map[string]string{"error": "Nama liga tidak dikenal"})
		return
	}
	if !daftarTier[body.Tier] {
		sendJSON(w, 400, map[string]string{"error": "Tier tidak valid"})
		return
	}

	memberID, err := getMemberIDByUsername(username)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "Akun ini belum tertaut ke data member"})
		return
	}

	col := "kalah"
	if body.Menang {
		col = "menang"
	}
	_, err = DB.Exec(fmt.Sprintf(`
		INSERT INTO liga_stats (member_id, liga, tier, %s) VALUES (?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE %s = %s + 1`, col, col, col),
		memberID, body.Liga, body.Tier)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	sendJSON(w, 200, map[string]string{"message": "Hasil liga tersimpan"})
}

// ─────────────────────────────────────────
//  HALAMAN: DAFTAR KATEGORI MATERI
// ─────────────────────────────────────────

func elCategoryListHandler(w http.ResponseWriter, r *http.Request) {
	_, _, role, _ := elCurrentUser(r)
	rows, err := DB.Query(`SELECT c.id, c.nama, COUNT(q.id)
		FROM el_categories c LEFT JOIN el_questions q ON q.category_id=c.id
		GROUP BY c.id ORDER BY c.id DESC`)
	var rowsHTML strings.Builder
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, jumlah int
			var nama string
			rows.Scan(&id, &nama, &jumlah)
			rowsHTML.WriteString(fmt.Sprintf(`<div class="card"><a class="cat-link" href="/elearning/kategori?id=%d">
				<div class="cat-name">📚 %s</div>
				<div class="cat-meta">%d soal</div></a></div>`, id, html.EscapeString(nama), jumlah))
		}
	}
	if rowsHTML.Len() == 0 {
		rowsHTML.WriteString(`<div class="empty">Belum ada kategori materi.</div>`)
	}
	adminLink := ""
	if role == "admin" {
		adminLink = `<a href="/elearning/admin">⚙ Kelola Materi</a>`
	}
	out := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>E-Learning</title>
<style>%s</style></head><body>
<h1>🎓 E-Learning</h1>
<div class="nav">
  <a href="/">🏠 Beranda</a>
  %s
  <a href="/logout">🚪 Logout</a>
</div>
<h2>Pilih Kategori</h2>
%s
</body></html>`, elPageStyle, adminLink, rowsHTML.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

// ─────────────────────────────────────────
//  HALAMAN: KUIS PER KATEGORI
// ─────────────────────────────────────────

func elQuizPageHandler(w http.ResponseWriter, r *http.Request) {
	catID := r.URL.Query().Get("id")
	if catID == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	var catNama string
	if err := DB.QueryRow("SELECT nama FROM el_categories WHERE id=?", catID).Scan(&catNama); err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	rows, err := DB.Query(`SELECT id, COALESCE(audio_path,''), pertanyaan,
		pilihan_a, pilihan_b, pilihan_c, pilihan_d, jawaban_benar
		FROM el_questions WHERE category_id=? ORDER BY id ASC`, catID)

	var soalJSON strings.Builder
	soalJSON.WriteString("[")
	first := true
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var qid int
			var audioPath, pertanyaan, a, b, c, d, benar string
			rows.Scan(&qid, &audioPath, &pertanyaan, &a, &b, &c, &d, &benar)
			if !first {
				soalJSON.WriteString(",")
			}
			first = false
			audioJS := "null"
			if audioPath != "" {
				audioJS = fmt.Sprintf(`"/elearning/audio?id=%d"`, qid)
			}
			soalJSON.WriteString(fmt.Sprintf(
				`{"id":%d,"audio":%s,"pertanyaan":%s,"pilihan":{"A":%s,"B":%s,"C":%s,"D":%s},"jawaban":%s}`,
				qid, audioJS, jsonStr(pertanyaan), jsonStr(a), jsonStr(b), jsonStr(c), jsonStr(d), jsonStr(benar)))
		}
	}
	soalJSON.WriteString("]")

	hasSoal := !first
	body := `<div class="empty">Belum ada soal di kategori ini.</div>`
	script := ""
	if hasSoal {
		body = `
<div class="progress" id="progress"></div>
<div class="qcard" id="qcard"></div>
<div style="text-align:center"><button id="nextBtn" style="display:none" onclick="next()">Lanjut →</button></div>`
		script = fmt.Sprintf(`
<script>
const SOAL = %s;
let idx = 0;
let selesai = false;

function render() {
  const card = document.getElementById('qcard');
  if (idx >= SOAL.length) {
    card.innerHTML = '<div class="empty">🎉 Selesai! Semua soal sudah dikerjakan.</div>';
    document.getElementById('progress').textContent = '';
    document.getElementById('nextBtn').style.display = 'none';
    return;
  }
  const q = SOAL[idx];
  document.getElementById('progress').textContent = 'Soal ' + (idx+1) + ' dari ' + SOAL.length;
  let audioHtml = q.audio ? '<audio controls src="' + q.audio + '"></audio>' : '';
  let optsHtml = '';
  for (const key of ['A','B','C','D']) {
    optsHtml += '<button class="opt" data-key="' + key + '" onclick="pilih(\'' + key + '\')">' + key + '. ' + q.pilihan[key] + '</button>';
  }
  card.innerHTML = '<div class="pertanyaan">' + q.pertanyaan + '</div>' + audioHtml + optsHtml;
  document.getElementById('nextBtn').style.display = 'none';
  selesai = false;
}

function pilih(key) {
  if (selesai) return;
  selesai = true;
  const q = SOAL[idx];
  document.querySelectorAll('.opt').forEach(btn => {
    if (btn.dataset.key === q.jawaban) btn.classList.add('correct');
    else if (btn.dataset.key === key && key !== q.jawaban) btn.classList.add('wrong');
    btn.onclick = null;
  });
  document.getElementById('nextBtn').style.display = 'block';
}

function next() { idx++; render(); }
render();
</script>`, soalJSON.String())
	}

	out := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>%s</style></head><body>
<h1>🎓 %s</h1>
<div class="nav">
  <a href="/elearning">← Daftar Kategori</a>
  <a href="/logout">🚪 Logout</a>
</div>
%s
%s
</body></html>`, html.EscapeString(catNama), elPageStyle, html.EscapeString(catNama), body, script)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func jsonStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	return `"` + s + `"`
}

func elAudioHandler(w http.ResponseWriter, r *http.Request) {
	qid := r.URL.Query().Get("id")
	var audioPath string
	if err := DB.QueryRow("SELECT COALESCE(audio_path,'') FROM el_questions WHERE id=?", qid).Scan(&audioPath); err != nil || audioPath == "" {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(audioPath, "/audio/")
	full := filepath.Join(AudioDir, rel)
	if !strings.HasPrefix(full, AudioDir) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}

// ─────────────────────────────────────────
//  ADMIN: KELOLA MATERI (kategori + soal)
// ─────────────────────────────────────────

func elAdminPageHandler(w http.ResponseWriter, r *http.Request) {
	selCat := r.URL.Query().Get("cat")
	msg := r.URL.Query().Get("msg")

	rows, _ := DB.Query(`SELECT c.id, c.nama, COUNT(q.id)
		FROM el_categories c LEFT JOIN el_questions q ON q.category_id=c.id
		GROUP BY c.id ORDER BY c.id DESC`)
	var catRows, catOptions strings.Builder
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id, jumlah int
			var nama string
			rows.Scan(&id, &nama, &jumlah)
			catRows.WriteString(fmt.Sprintf(`<tr><td>%d</td><td>%s</td><td>%d</td>
				<td><a class="del" href="/elearning/admin/delkategori?id=%d"
				onclick="return confirm('Hapus kategori ini beserta semua soalnya?')">🗑</a></td></tr>`,
				id, html.EscapeString(nama), jumlah, id))
			selected := ""
			if selCat == strconv.Itoa(id) {
				selected = " selected"
			}
			catOptions.WriteString(fmt.Sprintf(`<option value="%d"%s>%s</option>`, id, selected, html.EscapeString(nama)))
		}
	}
	if catRows.Len() == 0 {
		catRows.WriteString(`<tr><td colspan="4" style="text-align:center;color:#8b949e;padding:16px">Belum ada kategori</td></tr>`)
	}

	soalForm := ""
	if selCat != "" {
		qrows, _ := DB.Query(`SELECT id, COALESCE(audio_path,''), pertanyaan, jawaban_benar
			FROM el_questions WHERE category_id=? ORDER BY id ASC`, selCat)
		var qRows strings.Builder
		count := 0
		if qrows != nil {
			defer qrows.Close()
			for qrows.Next() {
				var qid int
				var audioPath, pertanyaan, benar string
				qrows.Scan(&qid, &audioPath, &pertanyaan, &benar)
				count++
				hasAudio := "—"
				if audioPath != "" {
					hasAudio = "🎵"
				}
				short := pertanyaan
				if len(short) > 40 {
					short = short[:40]
				}
				qRows.WriteString(fmt.Sprintf(`<tr><td>%d</td><td>%s</td>
					<td>%s</td><td>%s</td>
					<td><a class="del" href="/elearning/admin/delsoal?id=%d&cat=%s"
					onclick="return confirm('Hapus soal ini?')">🗑</a></td></tr>`,
					qid, hasAudio, html.EscapeString(short), html.EscapeString(benar), qid, selCat))
			}
		}
		if qRows.Len() == 0 {
			qRows.WriteString(`<tr><td colspan="5" style="text-align:center;color:#8b949e;padding:16px">Belum ada soal</td></tr>`)
		}
		soalForm = fmt.Sprintf(`
<h2>➕ Tambah Soal ke Kategori Ini</h2>
<div class="form-box">
<form method="POST" action="/elearning/admin/tambahsoal" enctype="multipart/form-data">
<input type="hidden" name="category_id" value="%s">
<label style="font-size:12px;color:#8b949e">File Audio (opsional)</label>
<input type="file" name="audio" accept="audio/*">
<label style="font-size:12px;color:#8b949e">Pertanyaan</label>
<textarea name="pertanyaan" rows="2" required></textarea>
<label style="font-size:12px;color:#8b949e">Pilihan A</label>
<input name="pilihan_a" required>
<label style="font-size:12px;color:#8b949e">Pilihan B</label>
<input name="pilihan_b" required>
<label style="font-size:12px;color:#8b949e">Pilihan C</label>
<input name="pilihan_c" required>
<label style="font-size:12px;color:#8b949e">Pilihan D</label>
<input name="pilihan_d" required>
<label style="font-size:12px;color:#8b949e">Jawaban Benar</label>
<select name="jawaban_benar">
<option value="A">A</option><option value="B">B</option>
<option value="C">C</option><option value="D">D</option>
</select>
<button type="submit">➕ Simpan Soal</button>
</form>
</div>
<h2>📋 Daftar Soal (%d)</h2>
<table>
<tr><th>ID</th><th>Audio</th><th>Pertanyaan</th><th>Jawaban</th><th>Hapus</th></tr>
%s
</table>`, selCat, count, qRows.String())
	}

	msgHTML := ""
	if msg != "" {
		msgHTML = fmt.Sprintf(`<div class="card" style="border-color:#3fb950;color:#3fb950">%s</div>`, html.EscapeString(msg))
	}

	out := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Kelola E-Learning</title>
<style>%s</style></head><body>
<h1>⚙ Kelola Materi E-Learning</h1>
<div class="nav">
  <a href="/elearning">← Lihat sebagai murid</a>
  <a href="/">🏠 Mimbar Bebas</a>
  <a href="/logout">🚪 Logout</a>
</div>
%s
<h2>➕ Tambah Kategori</h2>
<div class="form-box">
<form method="POST" action="/elearning/admin/tambahkategori">
<input name="nama" placeholder="Nama kategori, contoh: N4 Listening Pelajaran 1" required>
<button type="submit">➕ Tambah Kategori</button>
</form>
</div>

<h2>📚 Daftar Kategori</h2>
<table>
<tr><th>ID</th><th>Nama</th><th>Jml Soal</th><th>Hapus</th></tr>
%s
</table>

<h2>🔍 Pilih Kategori untuk Kelola Soal</h2>
<div class="form-box">
<form method="GET" action="/elearning/admin">
<select name="cat" onchange="this.form.submit()">
<option value="">-- pilih kategori --</option>
%s
</select>
</form>
</div>

%s
</body></html>`, elPageStyle, msgHTML, catRows.String(), catOptions.String(), soalForm)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func elAdminAddCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/elearning/admin", http.StatusFound)
		return
	}
	r.ParseForm()
	nama := strings.TrimSpace(r.FormValue("nama"))
	if nama != "" {
		DB.Exec("INSERT INTO el_categories (nama) VALUES (?)", nama)
	}
	http.Redirect(w, r, "/elearning/admin", http.StatusFound)
}

func elAdminDelCategory(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("id")
	if cid != "" {
		DB.Exec("DELETE FROM el_questions WHERE category_id=?", cid)
		DB.Exec("DELETE FROM el_categories WHERE id=?", cid)
	}
	http.Redirect(w, r, "/elearning/admin", http.StatusFound)
}

func elAdminAddQuestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/elearning/admin", http.StatusFound)
		return
	}
	r.ParseMultipartForm(20 << 20)
	catID := r.FormValue("category_id")
	pertanyaan := r.FormValue("pertanyaan")
	a := r.FormValue("pilihan_a")
	b := r.FormValue("pilihan_b")
	c := r.FormValue("pilihan_c")
	d := r.FormValue("pilihan_d")
	benar := r.FormValue("jawaban_benar")

	var audioURLPath string
	file, header, ferr := r.FormFile("audio")
	if ferr == nil {
		defer file.Close()
		saveName := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filepath.Base(header.Filename))
		dst, derr := os.Create(filepath.Join(AudioDir, saveName))
		if derr == nil {
			defer dst.Close()
			io.Copy(dst, file)
			audioURLPath = "/audio/" + saveName
		}
	}

	if catID != "" && pertanyaan != "" && a != "" && b != "" && c != "" && d != "" {
		var audioArg any
		if audioURLPath != "" {
			audioArg = audioURLPath
		} else {
			audioArg = nil
		}
		DB.Exec(`INSERT INTO el_questions
			(category_id, audio_path, pertanyaan, pilihan_a, pilihan_b, pilihan_c, pilihan_d, jawaban_benar)
			VALUES (?,?,?,?,?,?,?,?)`,
			catID, audioArg, pertanyaan, a, b, c, d, strings.ToUpper(benar))
	}
	http.Redirect(w, r, "/elearning/admin?cat="+catID, http.StatusFound)
}

func elAdminDelQuestion(w http.ResponseWriter, r *http.Request) {
	qid := r.URL.Query().Get("id")
	cat := r.URL.Query().Get("cat")
	if qid != "" {
		var audioPath string
		DB.QueryRow("SELECT COALESCE(audio_path,'') FROM el_questions WHERE id=?", qid).Scan(&audioPath)
		if audioPath != "" {
			rel := strings.TrimPrefix(audioPath, "/audio/")
			os.Remove(filepath.Join(AudioDir, rel))
		}
		DB.Exec("DELETE FROM el_questions WHERE id=?", qid)
	}
	http.Redirect(w, r, "/elearning/admin?cat="+cat, http.StatusFound)
}

// ─────────────────────────────────────────
//  AUTH (JSON API lama)
// ─────────────────────────────────────────

func apiElearningLogin(w http.ResponseWriter, r *http.Request) {
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
	var statusAkun string
	err := DB.QueryRow(
		`SELECT id, COALESCE(status_akun,'aktif') FROM users WHERE username=? AND password=SHA2(?,256)`,
		body.Username, body.Password,
	).Scan(&userID, &statusAkun)
	if err != nil {
		sendJSON(w, 401, map[string]string{"error": "Username atau password salah"})
		return
	}
	if statusAkun == "pending" {
		sendJSON(w, 403, map[string]string{"error": "Akun sedang menunggu konfirmasi admin"})
		return
	}
	if statusAkun == "cekal" {
		sendJSON(w, 403, map[string]string{"error": "Akun telah dinonaktifkan. Hubungi admin"})
		return
	}

	token := generateToken()
	expires := time.Now().Add(12 * time.Hour).Unix()
	DB.Exec("INSERT INTO sessions_elearning (token, user_id, expires_at) VALUES (?,?,?)",
		token, userID, expires)

	http.SetCookie(w, &http.Cookie{
		Name:    "el_token",
		Value:   token,
		Path:    "/",
		Expires: time.Now().Add(12 * time.Hour),
	})
	sendJSON(w, 200, map[string]string{"token": token, "message": "Login berhasil"})
}

func apiElearningLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("el_token")
	if err == nil {
		DB.Exec("DELETE FROM sessions_elearning WHERE token=?", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "el_token", Value: "", MaxAge: -1, Path: "/"})
	sendJSON(w, 200, map[string]string{"message": "Logout berhasil"})
}

// ─────────────────────────────────────────
//  KATEGORI (JSON API)
// ─────────────────────────────────────────

func apiELCategories(w http.ResponseWriter, r *http.Request) {
	type Cat struct {
		ID   int    `json:"id"`
		Nama string `json:"nama"`
	}
	rows, err := DB.Query("SELECT id, nama FROM el_categories ORDER BY id")
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []Cat
	for rows.Next() {
		var c Cat
		rows.Scan(&c.ID, &c.Nama)
		list = append(list, c)
	}
	if list == nil {
		list = []Cat{}
	}
	sendJSON(w, 200, list)
}

func apiELCategoryAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		Nama string `json:"nama"`
	}
	readJSON(r, &body)
	if strings.TrimSpace(body.Nama) == "" {
		sendJSON(w, 400, map[string]string{"error": "Nama kategori wajib"})
		return
	}
	res, err := DB.Exec("INSERT INTO el_categories (nama) VALUES (?)", strings.TrimSpace(body.Nama))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	sendJSON(w, 201, map[string]any{"id": id, "nama": body.Nama})
}

func apiELCategoryDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	readJSON(r, &body)
	DB.Exec("DELETE FROM el_categories WHERE id=?", body.ID)
	sendJSON(w, 200, map[string]string{"message": "Kategori dihapus"})
}

// ─────────────────────────────────────────
//  SOAL (JSON API)
// ─────────────────────────────────────────

type Question struct {
	ID           int    `json:"id"`
	CategoryID   int    `json:"category_id"`
	AudioPath    string `json:"audio_path"`
	Pertanyaan   string `json:"pertanyaan"`
	PilihanA     string `json:"pilihan_a"`
	PilihanB     string `json:"pilihan_b"`
	PilihanC     string `json:"pilihan_c"`
	PilihanD     string `json:"pilihan_d"`
	JawabanBenar string `json:"jawaban_benar"`
}

func apiELQuestions(w http.ResponseWriter, r *http.Request) {
	catID := r.URL.Query().Get("category_id")
	query := `SELECT id, category_id, COALESCE(audio_path,''), pertanyaan,
	           pilihan_a, pilihan_b, pilihan_c, pilihan_d, jawaban_benar
	           FROM el_questions`
	args := []any{}
	if catID != "" {
		query += " WHERE category_id=?"
		args = append(args, catID)
	}
	query += " ORDER BY id"

	rows, err := DB.Query(query, args...)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []Question
	for rows.Next() {
		var q Question
		rows.Scan(&q.ID, &q.CategoryID, &q.AudioPath, &q.Pertanyaan,
			&q.PilihanA, &q.PilihanB, &q.PilihanC, &q.PilihanD, &q.JawabanBenar)
		list = append(list, q)
	}
	if list == nil {
		list = []Question{}
	}
	sendJSON(w, 200, list)
}

func apiELQuestionAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var q Question
	readJSON(r, &q)
	if q.Pertanyaan == "" || q.CategoryID == 0 {
		sendJSON(w, 400, map[string]string{"error": "Pertanyaan dan category_id wajib"})
		return
	}
	res, err := DB.Exec(
		`INSERT INTO el_questions
		 (category_id, pertanyaan, pilihan_a, pilihan_b, pilihan_c, pilihan_d, jawaban_benar)
		 VALUES (?,?,?,?,?,?,?)`,
		q.CategoryID, q.Pertanyaan, q.PilihanA, q.PilihanB, q.PilihanC, q.PilihanD, q.JawabanBenar,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	q.ID = int(id)
	sendJSON(w, 201, q)
}

func apiELQuestionDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	readJSON(r, &body)
	var audioPath string
	DB.QueryRow("SELECT COALESCE(audio_path,'') FROM el_questions WHERE id=?", body.ID).Scan(&audioPath)
	if audioPath != "" {
		rel := strings.TrimPrefix(audioPath, "/audio/")
		os.Remove(filepath.Join(AudioDir, rel))
	}
	DB.Exec("DELETE FROM el_questions WHERE id=?", body.ID)
	sendJSON(w, 200, map[string]string{"message": "Soal dihapus"})
}

func apiELQuestionAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	r.ParseMultipartForm(10 << 20)
	questionID := r.FormValue("question_id")
	if questionID == "" {
		sendJSON(w, 400, map[string]string{"error": "question_id wajib"})
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "File audio tidak ada"})
		return
	}
	defer file.Close()

	saveName := fmt.Sprintf("q%s_%s", questionID, header.Filename)
	dst, err := os.Create(filepath.Join(AudioDir, saveName))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal simpan audio"})
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	urlPath := "/audio/" + saveName
	DB.Exec("UPDATE el_questions SET audio_path=? WHERE id=?", urlPath, questionID)
	sendJSON(w, 200, map[string]string{"audio_path": urlPath})
}

// ─────────────────────────────────────────
//  QUIZ (JSON API)
// ─────────────────────────────────────────

func apiELQuiz(w http.ResponseWriter, r *http.Request) {
	catID := r.URL.Query().Get("category_id")
	if catID == "" {
		sendJSON(w, 400, map[string]string{"error": "category_id wajib"})
		return
	}

	type QuizItem struct {
		ID         int    `json:"id"`
		AudioPath  string `json:"audio_path"`
		Pertanyaan string `json:"pertanyaan"`
		PilihanA   string `json:"pilihan_a"`
		PilihanB   string `json:"pilihan_b"`
		PilihanC   string `json:"pilihan_c"`
		PilihanD   string `json:"pilihan_d"`
	}

	rows, err := DB.Query(
		`SELECT id, COALESCE(audio_path,''), pertanyaan, pilihan_a, pilihan_b, pilihan_c, pilihan_d
		 FROM el_questions WHERE category_id=? ORDER BY RAND()`,
		catID,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []QuizItem
	for rows.Next() {
		var q QuizItem
		rows.Scan(&q.ID, &q.AudioPath, &q.Pertanyaan, &q.PilihanA, &q.PilihanB, &q.PilihanC, &q.PilihanD)
		list = append(list, q)
	}
	if list == nil {
		list = []QuizItem{}
	}
	sendJSON(w, 200, list)
}

func apiELSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		Answers []struct {
			ID      int    `json:"id"`
			Pilihan string `json:"pilihan"`
		} `json:"answers"`
	}
	readJSON(r, &body)

	benar := 0
	total := len(body.Answers)
	for _, a := range body.Answers {
		var jawaban string
		DB.QueryRow("SELECT jawaban_benar FROM el_questions WHERE id=?", a.ID).Scan(&jawaban)
		if strings.EqualFold(a.Pilihan, jawaban) {
			benar++
		}
	}

	var nilai float64
	if total > 0 {
		nilai = float64(benar) / float64(total) * 100
	}
	sendJSON(w, 200, map[string]any{
		"benar": benar,
		"total": total,
		"nilai": fmt.Sprintf("%.0f", nilai),
	})
}
