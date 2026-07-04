package main

import (
	"encoding/json"
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
//  STORAGE CONFIG
// ─────────────────────────────────────────

var StorageDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "uploads")
}()

// ─────────────────────────────────────────
//  MIDDLEWARE AUTH STORAGE
// ─────────────────────────────────────────

func authStorageMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Token")
		if token == "" {
			cookie, err := r.Cookie("storage_token")
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			token = cookie.Value
		}
		var userID int
		var expires int64
		err := DB.QueryRow(
			"SELECT user_id, expires_at FROM sessions_storage WHERE token=?", token,
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

func RegisterStorageRoutes(mux *http.ServeMux) {
	os.MkdirAll(StorageDir, 0755)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "storage.html")
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "storage_login.html")
	})
	mux.Handle("/uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(StorageDir))))

	mux.HandleFunc("/api/login", apiStorageLogin)
	mux.HandleFunc("/api/logout", apiStorageLogout)
	mux.HandleFunc("/api/files", authStorageMiddleware(apiStorageFiles))
	mux.HandleFunc("/api/upload", authStorageMiddleware(apiStorageUpload))
	mux.HandleFunc("/api/delete", authStorageMiddleware(apiStorageDelete))
}

// ─────────────────────────────────────────
//  API HANDLERS
// ─────────────────────────────────────────

func apiStorageLogin(w http.ResponseWriter, r *http.Request) {
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
	var scope string
	err := DB.QueryRow(
		`SELECT id, scope FROM users
		 WHERE username=? AND password=SHA2(?,256)
		 AND (scope='upload' OR scope='all')`,
		body.Username, body.Password,
	).Scan(&userID, &scope)
	if err != nil {
		sendJSON(w, 401, map[string]string{"error": "Username atau password salah"})
		return
	}

	token := generateToken()
	expires := time.Now().Add(24 * time.Hour).Unix()
	DB.Exec("INSERT INTO sessions_storage (token, user_id, expires_at) VALUES (?,?,?)",
		token, userID, expires)

	http.SetCookie(w, &http.Cookie{
		Name:    "storage_token",
		Value:   token,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})
	sendJSON(w, 200, map[string]string{"token": token, "message": "Login berhasil"})
}

func apiStorageLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("storage_token")
	if err == nil {
		DB.Exec("DELETE FROM sessions_storage WHERE token=?", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "storage_token", Value: "", MaxAge: -1, Path: "/"})
	sendJSON(w, 200, map[string]string{"message": "Logout berhasil"})
}

func apiStorageFiles(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	type FileItem struct {
		ID         int    `json:"id"`
		Filename   string `json:"filename"`
		Category   string `json:"category"`
		Path       string `json:"path"`
		UploadedAt string `json:"uploaded_at"`
	}

	query := "SELECT id, filename, category, path, uploaded_at FROM files"
	args := []any{}
	if category != "" {
		query += " WHERE category=?"
		args = append(args, category)
	}
	query += " ORDER BY uploaded_at DESC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var list []FileItem
	for rows.Next() {
		var f FileItem
		var t time.Time
		rows.Scan(&f.ID, &f.Filename, &f.Category, &f.Path, &t)
		f.UploadedAt = t.Format("2006-01-02 15:04:05")
		list = append(list, f)
	}
	if list == nil {
		list = []FileItem{}
	}
	sendJSON(w, 200, list)
}

func apiStorageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	r.ParseMultipartForm(50 << 20) // 50 MB

	category := r.FormValue("category")
	if category == "" {
		category = "umum"
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "File tidak ditemukan"})
		return
	}
	defer file.Close()

	filename := strings.ReplaceAll(header.Filename, " ", "_")
	saveName := time.Now().Format("20060102_150405_") + filename

	catDir := filepath.Join(StorageDir, category)
	os.MkdirAll(catDir, 0755)

	dst, err := os.Create(filepath.Join(catDir, saveName))
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": "Gagal simpan file"})
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	urlPath := "/uploads/" + category + "/" + saveName
	_, err = DB.Exec("INSERT INTO files (filename, category, path) VALUES (?,?,?)",
		filename, category, urlPath)
	if err != nil {
		log.Printf("[Storage] DB error: %v", err)
	}

	sendJSON(w, 201, map[string]any{
		"message":  "Upload berhasil",
		"filename": filename,
		"path":     urlPath,
		"category": category,
	})
}

func apiStorageDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == 0 {
		sendJSON(w, 400, map[string]string{"error": "ID tidak valid"})
		return
	}

	var filePath string
	DB.QueryRow("SELECT path FROM files WHERE id=?", body.ID).Scan(&filePath)
	if filePath != "" {
		rel := strings.TrimPrefix(filePath, "/uploads/")
		os.Remove(filepath.Join(StorageDir, rel))
	}
	DB.Exec("DELETE FROM files WHERE id=?", body.ID)
	sendJSON(w, 200, map[string]string{"message": "File dihapus"})
}

// ─────────────────────────────────────────
//  HELPER
// ─────────────────────────────────────────

func generateToken() string {
	b := make([]byte, 32)
	f, _ := os.Open("/dev/urandom")
	defer f.Close()
	f.Read(b)
	return fmt.Sprintf("%x", b)
}
