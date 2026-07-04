#!/data/data/com.termux/files/usr/bin/bash
# ─────────────────────────────────────────────────────────
#  update-server.sh
#  Copy file hasil edit (dari folder Download Termux) ke folder
#  proyek server, lalu build ulang & restart.
#
#  Sesuaikan SRC_DIR & PROJECT_DIR kalau lokasinya beda.
# ─────────────────────────────────────────────────────────

set -e

SRC_DIR="$HOME/storage/downloads"                     # tempat file hasil download dari Claude
PROJECT_DIR="/storage/emulated/0/Termux/server"        # folder proyek e-learning

FILES=(
  "member.go"
  "admin_edit.go"
  "admin_edit_panel.html"
)

echo "── Update Server: mulai ──"

for f in "${FILES[@]}"; do
  if [ -f "$SRC_DIR/$f" ]; then
    cp -f "$SRC_DIR/$f" "$PROJECT_DIR/$f"
    echo "✔ Copied: $f"
  else
    echo "⚠ Tidak ditemukan di $SRC_DIR: $f (lewati)"
  fi
done

cd "$PROJECT_DIR"

echo "── Cek sintaks Go (go vet) ──"
go vet ./... || { echo "✘ go vet gagal, build dibatalkan"; exit 1; }

echo "── Build ulang ──"
go build -o server-bin .

echo "── Hentikan server lama (jika berjalan) ──"
pkill -f "server-bin" 2>/dev/null || true
sleep 1

echo "── Jalankan server baru (background, log ke server.log) ──"
nohup ./server-bin > server.log 2>&1 &
disown

sleep 1
echo "── Selesai. Cek status: ──"
pgrep -fa "server-bin" || echo "⚠ Server tidak terdeteksi jalan, cek server.log"

echo "── Log terakhir: ──"
tail -n 20 server.log 2>/dev/null || true

echo "── Update Server: selesai ──"
