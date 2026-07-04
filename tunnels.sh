#!/data/data/com.termux/files/usr/bin/bash
# tunnels.sh — kelola cloudflared quick tunnel untuk myserver
#
# HANYA E-Learning (port 8081) yang di-expose ke publik.
# Storage (8080), WebRTC (8083), Admin (8084), dan MariaDB TIDAK online —
# tetap hanya bisa diakses secara lokal di HP ini.
#
# Pemakaian:
#   ./tunnels.sh start   -> jalankan tunnel E-Learning (kalau belum jalan)
#   ./tunnels.sh urls    -> tampilkan URL publik E-Learning yang aktif
#   ./tunnels.sh stop    -> matikan tunnel
#   ./tunnels.sh status  -> cek proses yang masih hidup

cd ~/myserver || { echo "Folder ~/myserver tidak ditemukan"; exit 1; }

LOGFILE="tunnel_elearning.log"

start_tunnel() {
  if pgrep -f "cloudflared tunnel --url http://localhost:8081" > /dev/null; then
    echo "Tunnel E-Learning sudah jalan. Pakai './tunnels.sh urls' buat lihat link."
    return
  fi

  # jaga-jaga: matikan tunnel lain kalau ada yang nyala (storage/webrtc/admin)
  # supaya tidak ada yang ke-expose selain e-learning
  pkill -f "cloudflared tunnel --url http://localhost:8080" 2>/dev/null
  pkill -f "cloudflared tunnel --url http://localhost:8083" 2>/dev/null
  pkill -f "cloudflared tunnel --url http://localhost:8084" 2>/dev/null

  echo "Menjalankan tunnel E-Learning (8081)..."
  cloudflared tunnel --url http://localhost:8081 > "$LOGFILE" 2>&1 &

  echo "Menunggu URL dibuat (5 detik)..."
  sleep 5
  show_url
}

show_url() {
  url=$(grep -h "trycloudflare.com" "$LOGFILE" 2>/dev/null | grep -o 'https://[a-zA-Z0-9.-]*trycloudflare.com' | tail -1)
  echo ""
  echo "=== URL E-Learning (satu-satunya yang online) ==="
  echo "${url:-belum tersedia / tunnel mati, coba ./tunnels.sh start}"
  echo ""
  echo "Storage, WebRTC, Admin, dan Database TETAP LOKAL (tidak online)."
}

stop_tunnel() {
  echo "Menghentikan tunnel..."
  pkill -f "cloudflared tunnel"
  echo "Selesai. Semua service kembali lokal-only."
}

status_tunnel() {
  echo "=== Proses cloudflared aktif ==="
  ps aux | grep "cloudflared tunnel" | grep -v grep
}

case "$1" in
  start)  start_tunnel ;;
  urls)   show_url ;;
  stop)   stop_tunnel ;;
  status) status_tunnel ;;
  *)
    echo "Pemakaian: ./tunnels.sh {start|urls|stop|status}"
    ;;
esac
