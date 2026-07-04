package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// ─────────────────────────────────────────
//  KONFIGURASI
// ─────────────────────────────────────────

const (
	PortStorage   = ":8080"
	PortElearning = ":8081"
	PortMember    = ":8082"
	PortSignaling = ":8083"
	PortAdminEdit = ":8084"
)

var (
	BaseDir = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "myserver")
	}()
)

func main() {
	// Init database
	InitDB()
	defer CloseDB()

	log.Println("╔══════════════════════════════════════════╗")
	log.Println("║  MyServer Go + WebRTC                    ║")
	log.Println("║  📁 Storage    → :8080                   ║")
	log.Println("║  🎓 E-Learning → :8081                   ║")
	log.Println("║  👥 Member     → :8082                   ║")
	log.Println("║  📡 WebRTC     → :8083                   ║")
	log.Println("║  👑 Admin Edit → :8084                   ║")
	log.Println("╚══════════════════════════════════════════╝")

	var wg sync.WaitGroup

	// Server Storage (8080)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		RegisterStorageRoutes(mux)
		log.Printf("[Storage] Listening on %s", PortStorage)
		if err := http.ListenAndServe(PortStorage, mux); err != nil {
			log.Fatalf("[Storage] Error: %v", err)
		}
	}()

	// Server E-Learning (8081)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		RegisterElearningRoutes(mux)
		log.Printf("[E-Learning] Listening on %s", PortElearning)
		if err := http.ListenAndServe(PortElearning, mux); err != nil {
			log.Fatalf("[E-Learning] Error: %v", err)
		}
	}()

	// Server Member (8082)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		RegisterMemberRoutes(mux)
		log.Printf("[Member] Listening on %s", PortMember)
		if err := http.ListenAndServe(PortMember, mux); err != nil {
			log.Fatalf("[Member] Error: %v", err)
		}
	}()

	// Server WebRTC Signaling (8083)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		RegisterWebRTCRoutes(mux)
		log.Printf("[WebRTC] Listening on %s", PortSignaling)
		if err := http.ListenAndServe(PortSignaling, mux); err != nil {
			log.Fatalf("[WebRTC] Error: %v", err)
		}
	}()

	// Server Admin Edit "My Lord" (8084)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		RegisterAdminEditRoutes(mux)
		log.Printf("[AdminEdit] Listening on %s", PortAdminEdit)
		if err := http.ListenAndServe(PortAdminEdit, mux); err != nil {
			log.Fatalf("[AdminEdit] Error: %v", err)
		}
	}()

	fmt.Println("\n✅ Semua server berjalan. Ctrl+C untuk stop.")
	wg.Wait()
}
