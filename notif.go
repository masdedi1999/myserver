package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ─────────────────────────────────────────
//  NOTIFIKASI — WEBSOCKET PUSH REALTIME
//
//  Hub ini terpisah dari room WebRTC (webrtc.go) tapi memakai
//  `upgrader` yang sama karena berada di package yang sama.
//  Setiap user yang login bisa punya lebih dari satu koneksi aktif
//  (mis. buka di 2 tab), makanya per-username disimpan sebagai slice.
// ─────────────────────────────────────────

// wsConnWrapper membungkus *websocket.Conn dengan mutex supaya aman
// ditulis dari goroutine ping keepalive dan goroutine pengirim notifikasi
// secara bersamaan (gorilla/websocket tidak thread-safe untuk write).
type wsConnWrapper struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsConnWrapper) writeJSON(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.TextMessage, data)
}

func (w *wsConnWrapper) writePing() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.PingMessage, nil)
}

type NotifMessage struct {
	Type  string `json:"type"`            // selalu "notif"
	Title string `json:"title"`
	Body  string `json:"body"`
	Time  string `json:"time"`
}

type notifHub struct {
	mu      sync.RWMutex
	clients map[string][]*notifClient // username → daftar koneksi aktif
}

type notifClient struct {
	conn *wsConnWrapper
}

var NotifHub = &notifHub{
	clients: make(map[string][]*notifClient),
}

func (h *notifHub) add(username string, c *notifClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[username] = append(h.clients[username], c)
}

func (h *notifHub) remove(username string, c *notifClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.clients[username]
	for i, cl := range list {
		if cl == c {
			h.clients[username] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.clients[username]) == 0 {
		delete(h.clients, username)
	}
}

// KirimNotifKe mengirim notifikasi realtime ke seorang user (semua tab/
// koneksi aktif miliknya). Panggil ini dari mana saja di kode server
// (mis. setelah ada tugas baru, pesan baru, dsb).
//
//	KirimNotifKe("budi", "Tugas Baru", "Pengajar menambahkan tugas Bab 3")
func KirimNotifKe(username, title, body string) {
	msg := NotifMessage{
		Type:  "notif",
		Title: title,
		Body:  body,
		Time:  time.Now().Format("15:04"),
	}
	data, _ := json.Marshal(msg)

	NotifHub.mu.RLock()
	targets := append([]*notifClient{}, NotifHub.clients[username]...)
	NotifHub.mu.RUnlock()

	for _, c := range targets {
		c.conn.writeJSON(data)
	}
}

// KirimNotifSemua broadcast ke seluruh user yang sedang online.
func KirimNotifSemua(title, body string) {
	msg := NotifMessage{
		Type:  "notif",
		Title: title,
		Body:  body,
		Time:  time.Now().Format("15:04"),
	}
	data, _ := json.Marshal(msg)

	NotifHub.mu.RLock()
	defer NotifHub.mu.RUnlock()
	for _, list := range NotifHub.clients {
		for _, c := range list {
			c.conn.writeJSON(data)
		}
	}
}

// ─────────────────────────────────────────
//  ROUTE
// ─────────────────────────────────────────

// RegisterNotifRoutes didaftarkan di mux yang sama dengan E-Learning (8081).
func RegisterNotifRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/notif", handleNotifWS)
}

func handleNotifWS(w http.ResponseWriter, r *http.Request) {
	// Sama seperti wsAuthenticatedUsername di webrtc.go: username diambil
	// dari sesi login server-side, tidak boleh dipalsukan lewat query param.
	username, ok := wsAuthenticatedUsername(r)
	if !ok {
		http.Error(w, "harus login untuk menerima notifikasi", 401)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Notif] Upgrade error: %v", err)
		return
	}

	wrapped := &wsConnWrapper{conn: conn}
	client := &notifClient{conn: wrapped}

	NotifHub.add(username, client)
	log.Printf("[Notif] %s tersambung ke kanal notifikasi", username)

	defer func() {
		NotifHub.remove(username, client)
		conn.Close()
		log.Printf("[Notif] %s terputus dari kanal notifikasi", username)
	}()

	conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Ping keepalive supaya koneksi tidak ditutup oleh proxy/browser saat idle.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := wrapped.writePing(); err != nil {
				return
			}
		}
	}()

	// Kanal ini murni push dari server ke client, jadi kita cuma perlu
	// terus membaca supaya bisa mendeteksi client putus/menutup koneksi.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
