# ecoharmonogram-ics

Harmonogram wywozu odpadów z aplikacji [EcoHarmonogram](https://ecoharmonogram.pl) jako kalendarz `.ics`, który można zasubskrybować w Google Calendar, Apple Calendar, Thunderbirdzie i innych.

Jedna statyczna binarka w Go, bez zależności:

- `ecocal fetch` uruchamiany z crona pobiera harmonogram i zapisuje plik `.ics`. Uwzględnia wszystkie okresy dostępne w API, więc harmonogram na nowy rok pojawi się sam, kiedy gmina go opublikuje.
- `ecocal serve` serwuje ten plik pod jedną, losową ścieżką. Ma limit zapytań na IP, obsługuje `ETag` i 304 i zużywa około 7 MB RAM.
- `ecocal lookup` pomaga znaleźć id gminy i nazwy ulic.

> EcoHarmonogram nie publikuje oficjalnego API. Narzędzie korzysta z tego samego endpointu co aplikacja mobilna, więc może przestać działać po zmianach po ich stronie. Jeśli `fetch` się nie powiedzie, poprzedni plik zostaje i nadal jest serwowany.

## Znajdź swoją miejscowość i ulicę

```sh
ecocal lookup -town Osieczna
# Osieczna  (community=127, gm. Osieczna, woj. Wielkopolskie)
#
# Okres 2026-01-01 – 2026-12-31, ulice:
#   ...
```

Jeśli nazwa miejscowości występuje w kilku gminach, dodaj `-community <id>`.

## Instalacja na serwerze (systemd + cron)

Na Linuksie (amd64 albo arm64) z systemd, cronem i curlem:

```sh
curl -fsSLO https://raw.githubusercontent.com/wChoros/ecoharmonogram-ics/main/deploy/install.sh
less install.sh   # warto przeczytać przed uruchomieniem
sudo sh install.sh -t "Osieczna" -c 127 -s "Kopernika" -p 8787
```

Skrypt:

1. pobiera binarkę z najnowszego Release, sprawdza SHA256 i instaluje ją do `/usr/local/bin/ecocal`;
2. tworzy użytkownika systemowego `ecocal`, katalog `/var/lib/ecocal` i konfigurację `/etc/ecocal/ecocal.env` z losowym tokenem w ścieżce URL;
3. pobiera harmonogram po raz pierwszy;
4. instaluje usługę `ecocal.service` (z utwardzeniem i `MemoryMax=24M`) oraz wpis `/etc/cron.d/ecocal` (codziennie o 04:17);
5. wypisuje adres kalendarza: `http://<IP>:8787/<token>.ics`.

Firewalla skrypt nie zmienia. Port trzeba otworzyć samemu, np. `sudo ufw allow 8787/tcp`, a także w panelu dostawcy VPS, jeśli ma firewall sieciowy.

Ponowne uruchomienie skryptu aktualizuje binarkę, unit i crona. Konfiguracji nie nadpisuje.

### Konfiguracja

`/etc/ecocal/ecocal.env`:

| Zmienna | Znaczenie | Domyślnie |
|---|---|---|
| `ECOCAL_TOWN` | miejscowość | – |
| `ECOCAL_COMMUNITY` | id gminy z `lookup` | – |
| `ECOCAL_STREET` | ulica | – |
| `ECOCAL_NAME` | nazwa kalendarza | `Wywóz śmieci` |
| `ECOCAL_PAYMENTS` | `true` dodaje terminy płatności | `false` |
| `ECOCAL_START` | godzina wydarzenia `HH:MM` (czas polski) albo `all-day` | `06:00` |
| `ECOCAL_DURATION` | czas trwania wydarzenia, np. `1h`, `30m` | `1h` |
| `ECOCAL_FILE` | plik `.ics` | `/var/lib/ecocal/calendar.ics` |
| `ECOCAL_ADDR` | adres nasłuchu | `:8787` |
| `ECOCAL_PATH` | ścieżka URL (sekretna) | losowy token |
| `ECOCAL_RATE` | żądań na godzinę na IP | `30` |
| `ECOCAL_BURST` | żądań naraz | `10` |

Po zmianie: `sudo systemctl restart ecocal` i, jeśli zmieniła się miejscowość albo ulica, ręczne pobranie (komenda niżej).

### Przydatne komendy

```sh
systemctl status ecocal                       # stan serwera
journalctl -u ecocal                          # logi serwera
journalctl -t ecocal                          # logi z crona
sudo runuser -u ecocal -- sh -c 'set -a; . /etc/ecocal/ecocal.env; exec ecocal fetch'   # pobierz teraz
```

## Subskrypcja

Każde wydarzenie ma przypomnienie o 19:00 dzień wcześniej.

**Apple Calendar (macOS):** Plik → **Nowa subskrypcja kalendarza** → wklej `http://<IP>:<port>/<token>.ics`. W oknie ustawień odznacz **Usuń: Alarmy**, żeby przypomnienia działały, i ustaw automatyczne odświeżanie, np. co godzinę. Jeśli jako miejsce wybierzesz iCloud, kalendarz pojawi się też na iPhonie. Na iPhonie: Ustawienia → Kalendarz → Konta → Dodaj konto → Inne → **Dodaj subskrybowany kalendarz**.

**Google Calendar:** Ustawienia → **Dodaj kalendarz** → **Z adresu URL**. Google odświeża subskrypcje co kilka do kilkunastu godzin i ignoruje alarmy z pliku, więc powiadomienia ustawia się w ustawieniach kalendarza.

## Limit zapytań

Token bucket na klienta: adres IPv4 albo cała sieć /64 dla IPv6. Po przekroczeniu limitu serwer odpowiada `429` z nagłówkiem `Retry-After`. Tabela klientów ma sztywny limit 4096 wpisów i jest co minutę czyszczona, więc zużycie pamięci nie rośnie przy zalewie żądań. Każda inna ścieżka niż `ECOCAL_PATH` dostaje `404`.

## Budowanie

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ecocal .
go test ./...
```

Release (binarki `linux/amd64` i `linux/arm64` + `checksums.txt`) buduje GitHub Actions po wypchnięciu tagu `v*`.

## Licencja

MIT
