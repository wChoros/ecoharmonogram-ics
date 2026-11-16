#!/bin/sh
# Instalacja ecoharmonogram-ics na serwerze z systemd i cronem.
#
#   curl -fsSLO https://raw.githubusercontent.com/wChoros/ecoharmonogram-ics/main/deploy/install.sh
#   sudo sh install.sh -t "Miejscowość" -c <id gminy> -s "Ulica" [-p 8787] [-n "Nazwa kalendarza"] [-v v0.1.0]
#
# Ponowne uruchomienie aktualizuje binarkę, unit i crona; istniejącej konfiguracji
# (/etc/ecocal/ecocal.env) nie nadpisuje. Firewalla nie zmienia.
set -eu

REPO="wChoros/ecoharmonogram-ics"
BIN=/usr/local/bin/ecocal
CONF_DIR=/etc/ecocal
ENV_FILE=$CONF_DIR/ecocal.env
DATA_DIR=/var/lib/ecocal
UNIT=/etc/systemd/system/ecocal.service
CRON=/etc/cron.d/ecocal

TOWN="" COMMUNITY="" STREET="" PORT=8787 NAME="Wywóz śmieci" VERSION=""

die() { echo "BŁĄD: $*" >&2; exit 1; }
usage() { sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

while getopts "t:c:s:p:n:v:h" opt; do
	case $opt in
	t) TOWN=$OPTARG ;;
	c) COMMUNITY=$OPTARG ;;
	s) STREET=$OPTARG ;;
	p) PORT=$OPTARG ;;
	n) NAME=$OPTARG ;;
	v) VERSION=$OPTARG ;;
	*) usage ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "uruchom przez sudo"
command -v systemctl >/dev/null || die "wymagany systemd"
command -v curl >/dev/null || die "wymagany curl"
command -v sha256sum >/dev/null || die "wymagany sha256sum"
[ -d /etc/cron.d ] && { command -v cron >/dev/null || command -v crond >/dev/null; } ||
	die "brak crona: zainstaluj go (np. apt install cron) i uruchom skrypt ponownie"

case $(uname -m) in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "nieobsługiwana architektura $(uname -m)" ;;
esac

# Wartości trafiają do pliku env czytanego przez sh i systemd: bez znaków specjalnych.
safe() {
	case $1 in *[\"\\\$\`]* | *"
"*) die "niedozwolony znak w wartości: $1" ;; esac
}

if [ -z "$VERSION" ]; then
	VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
	VERSION=${VERSION##*/}
fi
case $VERSION in v*) ;; *) die "nie udało się ustalić wersji (otrzymano: $VERSION)" ;; esac
echo "==> ecocal $VERSION ($ARCH)"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
REL="https://github.com/$REPO/releases/download/$VERSION"
RAW="https://raw.githubusercontent.com/$REPO/$VERSION/deploy"
curl -fsSL -o "$TMP/ecocal-linux-$ARCH" "$REL/ecocal-linux-$ARCH"
curl -fsSL -o "$TMP/checksums.txt" "$REL/checksums.txt"
(cd "$TMP" && grep " ecocal-linux-$ARCH\$" checksums.txt | sha256sum -c -) || die "suma SHA256 się nie zgadza"
curl -fsSL -o "$TMP/ecocal.service" "$RAW/ecocal.service"
curl -fsSL -o "$TMP/ecocal.cron" "$RAW/ecocal.cron"

install -m 0755 "$TMP/ecocal-linux-$ARCH" "$BIN"

if ! id ecocal >/dev/null 2>&1; then
	NOLOGIN=$(command -v nologin || echo /bin/false)
	useradd --system --no-create-home --home-dir "$DATA_DIR" --shell "$NOLOGIN" ecocal
fi
install -d -o ecocal -g ecocal -m 0755 "$DATA_DIR"
install -d -o root -g ecocal -m 0750 "$CONF_DIR"

if [ -f "$ENV_FILE" ]; then
	echo "==> zostawiam istniejącą konfigurację $ENV_FILE"
	[ -z "$TOWN$STREET$COMMUNITY" ] || echo "    (podane -t/-c/-s zignorowane; zmień je w $ENV_FILE)"
else
	[ -n "$TOWN" ] && [ -n "$STREET" ] || die "przy pierwszej instalacji podaj -t i -s (oraz -c, gdy nazwa się powtarza)"
	for v in "$TOWN" "$COMMUNITY" "$STREET" "$NAME" "$PORT"; do safe "$v"; done
	case $PORT in *[!0-9]* | "") die "zły port: $PORT" ;; esac
	if command -v openssl >/dev/null; then
		TOKEN=$(openssl rand -hex 16)
	else
		TOKEN=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
	fi
	umask 027
	cat >"$ENV_FILE" <<EOF
ECOCAL_TOWN="$TOWN"
ECOCAL_COMMUNITY="$COMMUNITY"
ECOCAL_STREET="$STREET"
ECOCAL_NAME="$NAME"
ECOCAL_FILE="$DATA_DIR/calendar.ics"
ECOCAL_ADDR=":$PORT"
ECOCAL_PATH="/$TOKEN.ics"
ECOCAL_RATE="30"
ECOCAL_BURST="10"
EOF
	chown root:ecocal "$ENV_FILE"
	chmod 0640 "$ENV_FILE"
	echo "==> utworzono $ENV_FILE"
fi

echo "==> pierwsze pobranie harmonogramu"
runuser -u ecocal -- sh -c "set -a; . $ENV_FILE; set +a; exec $BIN fetch" ||
	die "fetch się nie powiódł: sprawdź nazwy w $ENV_FILE (pomoże: $BIN lookup -town <nazwa>)"

install -m 0644 "$TMP/ecocal.service" "$UNIT"
install -m 0644 "$TMP/ecocal.cron" "$CRON"
systemctl daemon-reload
systemctl enable ecocal.service >/dev/null
systemctl restart ecocal.service
sleep 1
systemctl is-active --quiet ecocal.service || {
	journalctl -u ecocal.service -n 20 --no-pager
	die "usługa nie wystartowała"
}

. "$ENV_FILE"
PORT_NOW=${ECOCAL_ADDR##*:}
IP=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
echo
echo "Gotowe. Kalendarz:  http://${IP:-<IP-serwera>}:$PORT_NOW$ECOCAL_PATH"
echo
echo "Następne kroki:"
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
	echo "  1. Otwórz port:   sudo ufw allow $PORT_NOW/tcp"
else
	echo "  1. Otwórz port $PORT_NOW/tcp w firewallu (jeśli jakiś działa)."
fi
echo "     Jeśli dostawca VPS ma firewall sieciowy (np. w panelu OVH), dodaj tam regułę."
echo "  2. Sprawdź z zewnątrz:  curl -I http://${IP:-<IP-serwera>}:$PORT_NOW$ECOCAL_PATH"
echo "  3. Apple Calendar: Plik → Nowa subskrypcja kalendarza."
echo "     Google Calendar: Inne kalendarze → + → Z adresu URL."
