package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ─────────────────────────────────────────
//  WEBSOCKET UPGRADER
// ─────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ─────────────────────────────────────────
//  KONFIGURASI ROOM
// ─────────────────────────────────────────

// RoomMaxUsers = kapasitas maksimal per room (mis. Cafetaria: 5 orang).
const RoomMaxUsers = 5

// ─────────────────────────────────────────
//  MODEL
// ─────────────────────────────────────────

type Client struct {
	conn   *websocket.Conn
	roomID string
	userID string
	mu     sync.Mutex
}

type Room struct {
	clients   map[string]*Client // userID → Client
	joinOrder []string           // urutan userID join (untuk tentukan Room Master berikutnya)
	master    string             // userID yang sedang jadi Room Master (orang pertama yang join)
	music     MusicState         // status pemutaran musik yang sedang sinkron di room ini
	mu        sync.RWMutex
}

// MusicState = status pemutaran musik yang disiarkan (broadcast) ke semua
// client di room supaya semua orang dengar lagu yang sama di posisi yang
// (mendekati) sama. Hanya Room Master yang boleh mengubah ini.
type MusicState struct {
	TrackID     int     `json:"track_id"`     // id lagu di tabel room_music, 0 = belum ada lagu dipilih
	Title       string  `json:"title"`
	Playing     bool    `json:"playing"`
	PositionSec float64 `json:"position_sec"` // posisi terakhir diketahui (detik)
	UpdatedAtMs int64   `json:"updated_at_ms"` // waktu server saat PositionSec dicatat (epoch ms) -> client hitung offset berjalan
}

type Signal struct {
	Type    string          `json:"type"`              // offer, answer, candidate, join, leave, chat
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`      // kosong = broadcast ke semua
	RoomID  string          `json:"room_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ─────────────────────────────────────────
//  ROOM MANAGER
// ─────────────────────────────────────────

var (
	rooms   = make(map[string]*Room)
	roomsMu sync.RWMutex
)

// ─────────────────────────────────────────
//  NAMA ROOM (in-memory, khusus Cafetaria)
// ─────────────────────────────────────────
// Nama room bisa diubah lewat long-press di UI (lihat /api/room/rename).
// Disimpan hanya di memory server -> reset ke default kalau server restart.

var (
	roomNames   = make(map[string]string) // roomID -> nama custom
	roomNamesMu sync.RWMutex
)

func getRoomName(roomID, fallback string) string {
	roomNamesMu.RLock()
	defer roomNamesMu.RUnlock()
	if n, ok := roomNames[roomID]; ok && n != "" {
		return n
	}
	return fallback
}

func setRoomName(roomID, name string) {
	roomNamesMu.Lock()
	defer roomNamesMu.Unlock()
	roomNames[roomID] = name
}


func getOrCreateRoom(roomID string) *Room {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	if r, ok := rooms[roomID]; ok {
		return r
	}
	r := &Room{clients: make(map[string]*Client)}
	rooms[roomID] = r
	return r
}

func removeRoom(roomID string) {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	delete(rooms, roomID)
}

func (room *Room) broadcast(msg Signal, excludeUserID string) {
	room.mu.RLock()
	defer room.mu.RUnlock()
	data, _ := json.Marshal(msg)
	for uid, c := range room.clients {
		if uid == excludeUserID {
			continue
		}
		c.mu.Lock()
		c.conn.WriteMessage(websocket.TextMessage, data)
		c.mu.Unlock()
	}
}

func (room *Room) sendTo(userID string, msg Signal) {
	room.mu.RLock()
	c, ok := room.clients[userID]
	room.mu.RUnlock()
	if !ok {
		return
	}
	data, _ := json.Marshal(msg)
	c.mu.Lock()
	c.conn.WriteMessage(websocket.TextMessage, data)
	c.mu.Unlock()
}

func (room *Room) listUsers() []string {
	room.mu.RLock()
	defer room.mu.RUnlock()
	var users []string
	for uid := range room.clients {
		users = append(users, uid)
	}
	if users == nil {
		users = []string{}
	}
	return users
}

// count = jumlah user yang sedang berada di room saat ini.
func (room *Room) count() int {
	room.mu.RLock()
	defer room.mu.RUnlock()
	return len(room.clients)
}

// removeMember mengeluarkan client dari room. Kalau yang keluar adalah
// Room Master, master otomatis pindah ke orang berikutnya yang masih ada
// di room (berdasarkan urutan join paling lama yang tersisa).
// Mengembalikan (masterBerubah, masterBaru).
func (room *Room) removeMember(userID string) (bool, string) {
	room.mu.Lock()
	defer room.mu.Unlock()
	delete(room.clients, userID)
	for i, uid := range room.joinOrder {
		if uid == userID {
			room.joinOrder = append(room.joinOrder[:i], room.joinOrder[i+1:]...)
			break
		}
	}
	if room.master != userID {
		return false, room.master
	}
	// Cari master baru: orang tersisa dengan urutan join paling awal.
	room.master = ""
	for _, uid := range room.joinOrder {
		if _, ok := room.clients[uid]; ok {
			room.master = uid
			break
		}
	}
	return true, room.master
}

func (room *Room) getMaster() string {
	room.mu.RLock()
	defer room.mu.RUnlock()
	return room.master
}

// ─────────────────────────────────────────
//  AUTH — HANYA USERNAME ASLI (SESSION LOGIN)
// ─────────────────────────────────────────

// wsAuthenticatedUsername mengecek cookie sesi "el_token" milik user yang
// sedang login (sama seperti elCurrentUser di elearning.go) dan mengembalikan
// username asli miliknya. Ini mencegah orang menyambung ke /ws dengan
// parameter ?user=bebas tanpa login terlebih dahulu.
func wsAuthenticatedUsername(r *http.Request) (string, bool) {
	c, err := r.Cookie("el_token")
	if err != nil {
		return "", false
	}
	var username string
	err = DB.QueryRow(`SELECT u.username FROM sessions_elearning s
		JOIN users u ON s.user_id=u.id WHERE s.token=? AND s.expires_at>?`,
		c.Value, time.Now().Unix()).Scan(&username)
	if err != nil {
		return "", false
	}
	return username, true
}

// ─────────────────────────────────────────
//  MUSIK ROOM (mp3 diupload Room Master, disimpan sebagai file di server)
// ─────────────────────────────────────────

var MusicDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "myserver", "music")
}()

// ─────────────────────────────────────────
//  ROUTES
// ─────────────────────────────────────────

func RegisterWebRTCRoutes(mux *http.ServeMux) {
	os.MkdirAll(MusicDir, 0755)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "webrtc.html")
	})

	// WebSocket signaling
	mux.HandleFunc("/ws", handleWebRTCWS)

	// File musik yang sudah diupload (diputar lewat <audio src="/music-files/...">)
	mux.Handle("/music-files/", http.StripPrefix("/music-files/",
		http.FileServer(http.Dir(MusicDir))))

	// REST API
	mux.HandleFunc("/api/rooms", apiRoomList)
	mux.HandleFunc("/api/room/create", apiRoomCreate)
	mux.HandleFunc("/api/room/close", apiRoomClose)
	mux.HandleFunc("/api/room/names", apiRoomNames)
	mux.HandleFunc("/api/room/rename", apiRoomRename)
	mux.HandleFunc("/api/room/music/list", apiRoomMusicList)
	mux.HandleFunc("/api/room/music/upload", apiRoomMusicUpload)
	mux.HandleFunc("/api/room/music/delete", apiRoomMusicDelete)
}

// ─────────────────────────────────────────
//  WEBSOCKET HANDLER
// ─────────────────────────────────────────

func handleWebRTCWS(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		http.Error(w, "room wajib", 400)
		return
	}

	// Hanya username asli (sudah login) yang boleh masuk room.
	// Parameter ?user= dari client diabaikan; username diambil dari sesi
	// login server-side supaya tidak bisa dipalsukan.
	userID, ok := wsAuthenticatedUsername(r)
	if !ok {
		http.Error(w, "harus login dengan akun asli untuk masuk room", 401)
		return
	}

	// Cek kapasitas SEBELUM upgrade koneksi, supaya room yang penuh
	// langsung ditolak dengan status jelas tanpa buka socket dulu.
	room := getOrCreateRoom(roomID)
	room.mu.RLock()
	_, alreadyIn := room.clients[userID]
	full := len(room.clients) >= RoomMaxUsers && !alreadyIn
	room.mu.RUnlock()
	if full {
		http.Error(w, "room penuh (maksimal 5 orang)", 403)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebRTC] Upgrade error: %v", err)
		return
	}

	client := &Client{conn: conn, roomID: roomID, userID: userID}

	// Daftarkan client (cek kapasitas + tambah dalam SATU critical section
	// supaya tidak ada celah race condition antar koneksi yang masuk
	// bersamaan bisa sama-sama lolos cek dan melebihi kapasitas).
	room.mu.Lock()
	if _, already := room.clients[userID]; !already && len(room.clients) >= RoomMaxUsers {
		room.mu.Unlock()
		conn.WriteMessage(websocket.TextMessage, mustMarshal(Signal{
			Type:   "room_full",
			RoomID: roomID,
		}))
		conn.Close()
		return
	}
	room.clients[userID] = client
	alreadyInOrder := false
	for _, uid := range room.joinOrder {
		if uid == userID {
			alreadyInOrder = true
			break
		}
	}
	if !alreadyInOrder {
		room.joinOrder = append(room.joinOrder, userID)
	}
	if room.master == "" {
		room.master = userID
	}
	currentMaster := room.master
	room.mu.Unlock()

	log.Printf("[WebRTC] %s bergabung ke room %s (total: %d/%d, master: %s)",
		userID, roomID, room.count(), RoomMaxUsers, currentMaster)

	// Simpan ke DB
	DB.Exec(`INSERT IGNORE INTO webrtc_rooms (room_id, room_type, created_by)
	         VALUES (?, 'call', NULL)`, roomID)

	// Beritahu semua: ada user baru
	room.broadcast(Signal{
		Type:    "join",
		From:    userID,
		RoomID:  roomID,
		Payload: mustMarshal(map[string]any{"users": room.listUsers(), "master": currentMaster}),
	}, "")

	// Kirim status musik yang sedang berjalan (kalau ada) khusus ke user
	// yang baru join, supaya dia langsung ikut sinkron dari posisi terkini.
	room.mu.RLock()
	currentMusic := room.music
	room.mu.RUnlock()
	if currentMusic.TrackID != 0 {
		room.sendTo(userID, Signal{
			Type:    "music_state",
			RoomID:  roomID,
			Payload: mustMarshal(currentMusic),
		})
	}

	// Loop baca pesan
	defer func() {
		masterChanged, newMaster := room.removeMember(userID)
		room.mu.RLock()
		isEmpty := len(room.clients) == 0
		room.mu.RUnlock()

		conn.Close()

		room.broadcast(Signal{
			Type:    "leave",
			From:    userID,
			RoomID:  roomID,
			Payload: mustMarshal(map[string]any{"master": newMaster, "master_changed": masterChanged}),
		}, "")

		if isEmpty {
			DB.Exec("UPDATE webrtc_rooms SET closed_at=? WHERE room_id=?",
				time.Now(), roomID)
			removeRoom(roomID)
			log.Printf("[WebRTC] Room %s ditutup (kosong)", roomID)
		} else if masterChanged {
			log.Printf("[WebRTC] Room Master di %s pindah ke %s", roomID, newMaster)
		}
		log.Printf("[WebRTC] %s keluar dari room %s", userID, roomID)
	}()

	conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Ping keepalive
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			client.mu.Lock()
			err := conn.WriteMessage(websocket.PingMessage, nil)
			client.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var sig Signal
		if err := json.Unmarshal(raw, &sig); err != nil {
			continue
		}
		sig.From = userID
		sig.RoomID = roomID

		switch sig.Type {
		case "offer", "answer", "candidate":
			// Kirim ke user tertentu (P2P signaling)
			if sig.To != "" {
				room.sendTo(sig.To, sig)
			}
		case "chat":
			// Broadcast chat ke semua termasuk pengirim
			room.broadcast(sig, "")
		case "music_control":
			// Kontrol musik (play/pause/pilih lagu/berhenti) HANYA boleh
			// dikirim oleh Room Master. Selain itu diabaikan diam-diam.
			if room.getMaster() != userID {
				continue
			}
			var ms MusicState
			if err := json.Unmarshal(sig.Payload, &ms); err != nil {
				continue
			}
			ms.UpdatedAtMs = time.Now().UnixMilli()
			room.mu.Lock()
			room.music = ms
			room.mu.Unlock()
			room.broadcast(Signal{
				Type:    "music_state",
				From:    userID,
				RoomID:  roomID,
				Payload: mustMarshal(ms),
			}, "")
		default:
			// Broadcast sinyal lain
			room.broadcast(sig, userID)
		}
	}
}

// ─────────────────────────────────────────
//  REST API ROOMS
// ─────────────────────────────────────────

func apiRoomList(w http.ResponseWriter, r *http.Request) {
	type RoomInfo struct {
		RoomID    string   `json:"room_id"`
		RoomType  string   `json:"room_type"`
		CreatedAt string   `json:"created_at"`
		Users     int      `json:"users_online"`
		MaxUsers  int      `json:"max_users"`
		Usernames []string `json:"usernames"`
		Master    string   `json:"master"`
	}

	rows, err := DB.Query(
		`SELECT room_id, room_type, created_at FROM webrtc_rooms
		 WHERE closed_at IS NULL ORDER BY created_at DESC LIMIT 50`,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var list []RoomInfo
	for rows.Next() {
		var ri RoomInfo
		var t time.Time
		rows.Scan(&ri.RoomID, &ri.RoomType, &t)
		ri.CreatedAt = t.Format("2006-01-02 15:04:05")
		ri.MaxUsers = RoomMaxUsers
		ri.Usernames = []string{}
		// Hitung user online + daftar username asli yang sedang di room
		roomsMu.RLock()
		if rm, ok := rooms[ri.RoomID]; ok {
			ri.Usernames = rm.listUsers()
			ri.Users = len(ri.Usernames)
			ri.Master = rm.getMaster()
		}
		roomsMu.RUnlock()
		list = append(list, ri)
	}
	if list == nil {
		list = []RoomInfo{}
	}
	sendJSON(w, 200, list)
}

func apiRoomCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		RoomID   string `json:"room_id"`
		RoomType string `json:"room_type"`
	}
	readJSON(r, &body)
	if body.RoomID == "" {
		body.RoomID = generateToken()[:12]
	}
	if body.RoomType == "" {
		body.RoomType = "call"
	}
	_, err := DB.Exec(
		"INSERT IGNORE INTO webrtc_rooms (room_id, room_type) VALUES (?,?)",
		body.RoomID, body.RoomType,
	)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	sendJSON(w, 201, map[string]string{"room_id": body.RoomID, "room_type": body.RoomType})
}

func apiRoomClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	var body struct {
		RoomID string `json:"room_id"`
	}
	readJSON(r, &body)

	DB.Exec("UPDATE webrtc_rooms SET closed_at=? WHERE room_id=?", time.Now(), body.RoomID)

	// Tutup paksa semua koneksi di room
	roomsMu.Lock()
	if rm, ok := rooms[body.RoomID]; ok {
		rm.mu.Lock()
		for _, c := range rm.clients {
			c.conn.Close()
		}
		rm.mu.Unlock()
		delete(rooms, body.RoomID)
	}
	roomsMu.Unlock()

	sendJSON(w, 200, map[string]string{"message": "Room ditutup"})
}

// apiRoomNames mengembalikan nama custom (kalau ada) untuk room-room
// Cafetaria: {"cafetaria-1": "Room 1", "cafetaria-2": "Meja Nongkrong", ...}
func apiRoomNames(w http.ResponseWriter, r *http.Request) {
	defaults := map[string]string{
		"cafetaria-1": "Room 1",
		"cafetaria-2": "Room 2",
		"cafetaria-3": "Room 3",
	}
	out := make(map[string]string, len(defaults))
	for id, def := range defaults {
		out[id] = getRoomName(id, def)
	}
	sendJSON(w, 200, out)
}

// apiRoomRename mengganti nama sebuah room Cafetaria. Hanya username asli
// (sudah login lewat sesi elearning) yang boleh mengganti nama room.
func apiRoomRename(w http.ResponseWriter, r *http.Request) {
	// Endpoint ini dipanggil cross-origin dari E-Learning (:8081) ke sini
	// (:8083) DAN butuh cookie sesi login untuk validasi username asli.
	// Karena cookie ikut terkirim (credentials), header CORS tidak boleh
	// pakai wildcard "*" -- harus origin spesifik yang meminta.
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	if _, ok := wsAuthenticatedUsername(r); !ok {
		http.Error(w, "harus login dengan akun asli", 401)
		return
	}
	var body struct {
		RoomID string `json:"room_id"`
		Name   string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "body tidak valid", 400)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		http.Error(w, "nama tidak boleh kosong", 400)
		return
	}
	if len(name) > 24 {
		name = name[:24]
	}
	if body.RoomID == "" {
		http.Error(w, "room_id wajib", 400)
		return
	}
	setRoomName(body.RoomID, name)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"room_id": body.RoomID, "name": name})
}

// ─────────────────────────────────────────
//  MUSIK ROOM — LIST / UPLOAD / DELETE
// ─────────────────────────────────────────

// isRoomMaster mengecek apakah username tertentu adalah Room Master yang
// sedang aktif di room tersebut. Room harus punya koneksi WS aktif (baru
// ada konsep master kalau sudah ada yang join).
func isRoomMaster(roomID, username string) bool {
	roomsMu.RLock()
	rm, ok := rooms[roomID]
	roomsMu.RUnlock()
	if !ok {
		return false
	}
	return rm.getMaster() == username
}

func setCORSWithCredentials(w http.ResponseWriter, r *http.Request, methods string) {
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Access-Control-Allow-Methods", methods+", OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

type MusicItem struct {
	ID         int    `json:"id"`
	RoomID     string `json:"room_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	UploadedBy string `json:"uploaded_by"`
	UploadedAt string `json:"uploaded_at"`
}

// apiRoomMusicList mengembalikan daftar lagu yang sudah diupload untuk
// sebuah room. Semua orang yang login boleh melihat list (bukan cuma master).
func apiRoomMusicList(w http.ResponseWriter, r *http.Request) {
	setCORSWithCredentials(w, r, "GET")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		http.Error(w, "room_id wajib", 400)
		return
	}
	if _, ok := wsAuthenticatedUsername(r); !ok {
		http.Error(w, "harus login dengan akun asli", 401)
		return
	}
	rows, err := DB.Query(
		`SELECT id, room_id, title, file_path, uploaded_by, uploaded_at
		 FROM room_music WHERE room_id=? ORDER BY uploaded_at DESC`, roomID)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var list []MusicItem
	for rows.Next() {
		var m MusicItem
		var filePath string
		var t time.Time
		if err := rows.Scan(&m.ID, &m.RoomID, &m.Title, &filePath, &m.UploadedBy, &t); err != nil {
			continue
		}
		m.URL = filePath
		m.UploadedAt = t.Format("2006-01-02 15:04:05")
		list = append(list, m)
	}
	if list == nil {
		list = []MusicItem{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(list)
}

// apiRoomMusicUpload menerima file mp3 dari Room Master dan menyimpannya
// sebagai file fisik di server (bukan BLOB di database).
func apiRoomMusicUpload(w http.ResponseWriter, r *http.Request) {
	setCORSWithCredentials(w, r, "POST")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	username, ok := wsAuthenticatedUsername(r)
	if !ok {
		http.Error(w, "harus login dengan akun asli", 401)
		return
	}

	r.ParseMultipartForm(30 << 20) // maks 30 MB per file mp3
	roomID := r.FormValue("room_id")
	if roomID == "" {
		http.Error(w, "room_id wajib", 400)
		return
	}
	// Hanya Room Master yang boleh menambah lagu ke room.
	if !isRoomMaster(roomID, username) {
		http.Error(w, "hanya Room Master yang boleh menambah musik", 403)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file tidak ditemukan", 400)
		return
	}
	defer file.Close()

	lowerName := strings.ToLower(header.Filename)
	if !strings.HasSuffix(lowerName, ".mp3") {
		http.Error(w, "hanya file .mp3 yang diperbolehkan", 400)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	if len(title) > 120 {
		title = title[:120]
	}

	safeName := strings.ReplaceAll(header.Filename, " ", "_")
	saveName := time.Now().Format("20060102_150405_") + safeName
	roomDir := filepath.Join(MusicDir, roomID)
	if err := os.MkdirAll(roomDir, 0755); err != nil {
		http.Error(w, "gagal menyiapkan folder musik", 500)
		return
	}

	dst, err := os.Create(filepath.Join(roomDir, saveName))
	if err != nil {
		http.Error(w, "gagal menyimpan file", 500)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "gagal menulis file", 500)
		return
	}

	urlPath := "/music-files/" + roomID + "/" + saveName
	res, err := DB.Exec(
		`INSERT INTO room_music (room_id, title, file_path, uploaded_by) VALUES (?,?,?,?)`,
		roomID, title, urlPath, username)
	if err != nil {
		sendJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	sendJSON(w, 201, MusicItem{
		ID: int(id), RoomID: roomID, Title: title, URL: urlPath, UploadedBy: username,
	})
}

// apiRoomMusicDelete menghapus lagu dari room (file fisik + record DB).
// Hanya Room Master yang boleh menghapus.
func apiRoomMusicDelete(w http.ResponseWriter, r *http.Request) {
	setCORSWithCredentials(w, r, "POST")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	username, ok := wsAuthenticatedUsername(r)
	if !ok {
		http.Error(w, "harus login dengan akun asli", 401)
		return
	}
	var body struct {
		ID     int    `json:"id"`
		RoomID string `json:"room_id"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == 0 || body.RoomID == "" {
		http.Error(w, "id dan room_id wajib", 400)
		return
	}
	if !isRoomMaster(body.RoomID, username) {
		http.Error(w, "hanya Room Master yang boleh menghapus musik", 403)
		return
	}

	var filePath string
	err := DB.QueryRow("SELECT file_path FROM room_music WHERE id=? AND room_id=?",
		body.ID, body.RoomID).Scan(&filePath)
	if err != nil {
		http.Error(w, "lagu tidak ditemukan", 404)
		return
	}
	DB.Exec("DELETE FROM room_music WHERE id=?", body.ID)

	rel := strings.TrimPrefix(filePath, "/music-files/")
	os.Remove(filepath.Join(MusicDir, rel))

	sendJSON(w, 200, map[string]string{"message": "Lagu dihapus"})
}

// ─────────────────────────────────────────
//  HELPER
// ─────────────────────────────────────────

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
