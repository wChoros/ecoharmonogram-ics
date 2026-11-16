// ecocal pobiera harmonogram wywozu odpadów z EcoHarmonogram i serwuje go jako
// subskrybowany kalendarz iCalendar (.ics).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var version = "dev"

const usage = `ecocal: harmonogram EcoHarmonogram jako subskrybowany kalendarz .ics

Użycie:
  ecocal lookup -town <miejscowość> [-community <id>]   znajdź id gminy i nazwy ulic
  ecocal fetch  [flagi]                                  pobierz harmonogram do pliku .ics (dla crona)
  ecocal serve  [flagi]                                  serwuj plik .ics przez HTTP
  ecocal version

Każda flaga ma odpowiednik w zmiennej środowiskowej ECOCAL_<NAZWA>,
np. -town → ECOCAL_TOWN. Szczegóły: ecocal <polecenie> -h
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "fetch":
		err = cmdFetch(args)
	case "serve":
		err = cmdServe(args)
	case "lookup":
		err = cmdLookup(args)
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "nieznane polecenie %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Printf("błąd: %v", err)
		os.Exit(1)
	}
}

func env(name, def string) string {
	if v, ok := os.LookupEnv("ECOCAL_" + name); ok && v != "" {
		return v
	}
	return def
}

func envBool(name string) bool {
	b, _ := strconv.ParseBool(env(name, "false"))
	return b
}

func envFloat(name string, def float64) float64 {
	f, err := strconv.ParseFloat(env(name, ""), 64)
	if err != nil {
		return def
	}
	return f
}

func cmdFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	town := fs.String("town", env("TOWN", ""), "miejscowość (ECOCAL_TOWN)")
	community := fs.String("community", env("COMMUNITY", ""), "id gminy z `ecocal lookup` (ECOCAL_COMMUNITY)")
	street := fs.String("street", env("STREET", ""), "ulica (ECOCAL_STREET)")
	payments := fs.Bool("payments", envBool("PAYMENTS"), "dołącz terminy płatności (ECOCAL_PAYMENTS)")
	name := fs.String("name", env("NAME", "Wywóz śmieci"), "nazwa kalendarza (ECOCAL_NAME)")
	out := fs.String("out", env("FILE", "calendar.ics"), "plik wyjściowy (ECOCAL_FILE)")
	start := fs.String("start", env("START", "06:00"), "godzina wydarzenia HH:MM albo all-day (ECOCAL_START)")
	duration := fs.String("duration", env("DURATION", "1h"), "czas trwania wydarzenia (ECOCAL_DURATION)")
	_ = fs.Parse(args)
	if *town == "" || *street == "" {
		return errors.New("wymagane -town i -street")
	}
	et, err := ParseEventTime(*start, *duration)
	if err != nil {
		return err
	}

	towns, err := getTowns()
	if err != nil {
		return err
	}
	t, err := findTown(towns, *town, *community)
	if err != nil {
		return err
	}
	periods, err := getPeriods(t.ID)
	if err != nil {
		return err
	}
	if len(periods) == 0 {
		return errors.New("brak okresów harmonogramu dla tej miejscowości")
	}
	var all []Collection
	for _, p := range periods {
		streets, err := getStreets(t.ID, p.ID)
		if err != nil {
			return fmt.Errorf("okres %s–%s: %w", p.StartDate, p.EndDate, err)
		}
		s, err := findStreet(streets, *street)
		if err != nil {
			return fmt.Errorf("okres %s–%s: %w", p.StartDate, p.EndDate, err)
		}
		cs, err := getCollections(s.ID, p.ID)
		if err != nil {
			return fmt.Errorf("okres %s–%s: %w", p.StartDate, p.EndDate, err)
		}
		for _, c := range cs {
			if !*payments && strings.EqualFold(c.Kind, PaymentsKind) {
				continue
			}
			all = append(all, c)
		}
	}
	if len(all) == 0 {
		return errors.New("API nie zwróciło żadnych terminów; zostawiam poprzedni plik")
	}

	ics := []byte(BuildICS(*name, all, et))
	if old, err := os.ReadFile(*out); err == nil && bytes.Equal(old, ics) {
		return nil // bez zmian: nie ruszamy mtime, więc klienci dostają 304
	}
	if err := writeAtomic(*out, ics); err != nil {
		return err
	}
	log.Printf("zapisano %s: %d terminów, %d okres(ów)", *out, len(all), len(periods))
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ecocal-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("ADDR", ":8787"), "adres nasłuchu (ECOCAL_ADDR)")
	file := fs.String("file", env("FILE", "calendar.ics"), "serwowany plik .ics (ECOCAL_FILE)")
	path := fs.String("path", env("PATH", ""), "ścieżka URL, np. /<losowy-token>.ics (ECOCAL_PATH)")
	rate := fs.Float64("rate", envFloat("RATE", 30), "limit żądań na godzinę na IP (ECOCAL_RATE)")
	burst := fs.Int("burst", int(envFloat("BURST", 10)), "ile żądań naraz (ECOCAL_BURST)")
	_ = fs.Parse(args)
	if *path == "" {
		return errors.New("wymagane -path (np. /$(openssl rand -hex 16).ics)")
	}
	return runServe(*addr, *file, *path, *rate, *burst)
}

func cmdLookup(args []string) error {
	fs := flag.NewFlagSet("lookup", flag.ExitOnError)
	town := fs.String("town", "", "miejscowość (dopasowanie fragmentu)")
	community := fs.String("community", "", "id gminy, pokazuje też ulice")
	_ = fs.Parse(args)
	if *town == "" {
		return errors.New("wymagane -town")
	}
	towns, err := getTowns()
	if err != nil {
		return err
	}
	var matches []Town
	needle := strings.ToLower(*town)
	for _, t := range towns {
		if strings.Contains(strings.ToLower(t.Name), needle) && (*community == "" || t.CommunityID == *community) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("brak miejscowości pasujących do %q", *town)
	}
	for _, t := range matches {
		fmt.Printf("%s  (community=%s, gm. %s, woj. %s)\n", t.Name, t.CommunityID, t.District, t.Province)
	}
	if len(matches) > 1 {
		fmt.Println("\nPodaj dokładną nazwę i -community, aby zobaczyć ulice.")
		return nil
	}
	periods, err := getPeriods(matches[0].ID)
	if err != nil {
		return err
	}
	for _, p := range periods {
		streets, err := getStreets(matches[0].ID, p.ID)
		if err != nil {
			return err
		}
		fmt.Printf("\nOkres %s – %s, ulice:\n", p.StartDate, p.EndDate)
		for _, s := range streets {
			fmt.Printf("  %s\n", s.Name)
		}
	}
	return nil
}
