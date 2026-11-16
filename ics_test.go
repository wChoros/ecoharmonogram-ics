package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func d(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC) }

func TestFoldKeepsLinesShortAndUTF8Intact(t *testing.T) {
	long := "SUMMARY:" + strings.Repeat("żółć ", 40)
	folded := fold(long)
	if !strings.HasSuffix(folded, "\r\n") {
		t.Fatal("brak CRLF na końcu")
	}
	for i, l := range strings.Split(strings.TrimSuffix(folded, "\r\n"), "\r\n") {
		if len(l) > 75 {
			t.Errorf("linia %d ma %d oktetów", i, len(l))
		}
		if !utf8.ValidString(l) {
			t.Errorf("linia %d rozcina znak UTF-8: %q", i, l)
		}
		if i > 0 && !strings.HasPrefix(l, " ") {
			t.Errorf("linia kontynuacji %d nie zaczyna się spacją", i)
		}
	}
	unfolded := strings.ReplaceAll(strings.TrimSuffix(folded, "\r\n"), "\r\n ", "")
	if unfolded != long {
		t.Error("rozwinięcie nie odtwarza oryginału")
	}
}

func TestEscapeText(t *testing.T) {
	got := escapeText("a,b;c\\d\ne")
	want := `a\,b\;c\\d\ne`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSummary(t *testing.T) {
	if got := Summary("ODPADY NIESEGREGOWANE"); got != "⚫ Odpady niesegregowane" {
		t.Errorf("got %q", got)
	}
	if got := Summary("ŻUŻEL"); got != "🗑️ Żużel" {
		t.Errorf("got %q", got)
	}
}

func TestBuildICSDeterministicAndDeduped(t *testing.T) {
	cs := []Collection{
		{d(2026, 12, 21), "ODPADY BIODEGRADOWALNE"},
		{d(2026, 11, 17), "PAPIER, TWORZYWA SZTUCZNE, SZKŁO"},
		{d(2026, 12, 21), "ODPADY NIESEGREGOWANE"},
		{d(2026, 11, 17), "PAPIER, TWORZYWA SZTUCZNE, SZKŁO"}, // duplikat
	}
	a := BuildICS("Test", cs, EventTime{AllDay: true})
	b := BuildICS("Test", []Collection{cs[2], cs[0], cs[3], cs[1]}, EventTime{AllDay: true})
	if a != b {
		t.Error("wynik zależy od kolejności wejścia")
	}
	if n := strings.Count(a, "BEGIN:VEVENT"); n != 3 {
		t.Errorf("oczekiwano 3 wydarzeń, jest %d", n)
	}
	if !strings.Contains(a, "DTSTART;VALUE=DATE:20261117\r\nDTEND;VALUE=DATE:20261118\r\n") {
		t.Error("złe DTSTART/DTEND")
	}
	if !strings.Contains(a, `SUMMARY:🟡 Papier\, tworzywa sztuczne\, szkło`) {
		t.Error("brak escapowanego SUMMARY")
	}
	if strings.Index(a, "20261117") > strings.Index(a, "20261221") {
		t.Error("wydarzenia nieposortowane")
	}
	for _, l := range strings.Split(a, "\r\n") {
		if len(l) > 75 {
			t.Errorf("za długa linia: %q", l)
		}
	}
}

func TestFindStreet(t *testing.T) {
	streets := []Street{{ID: "1", Name: "Polna"}, {ID: "2", Name: "3 Maja"}, {ID: "3", Name: "Leśna"}, {ID: "4", Name: "Leśna", Sides: "x"}}
	if s, err := findStreet(streets, "3 maja"); err != nil || s.ID != "2" {
		t.Errorf("3 maja: %v %v", s, err)
	}
	if _, err := findStreet(streets, "Brak"); err == nil {
		t.Error("oczekiwano błędu dla brakującej ulicy")
	}
	if _, err := findStreet(streets, "Leśna"); err == nil {
		t.Error("oczekiwano błędu dla niejednoznacznej ulicy")
	}
}

func TestBuildICSTimedEvents(t *testing.T) {
	et, err := ParseEventTime("06:00", "1h")
	if err != nil {
		t.Fatal(err)
	}
	ics := BuildICS("Test", []Collection{{d(2026, 3, 29), "ODPADY NIESEGREGOWANE"}}, et)
	for _, want := range []string{
		"BEGIN:VTIMEZONE\r\nTZID:Europe/Warsaw\r\n",
		"DTSTART;TZID=Europe/Warsaw:20260329T060000\r\nDTEND;TZID=Europe/Warsaw:20260329T070000\r\n",
		"TRIGGER:-PT11H\r\n", // 19:00 dzień wcześniej
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("brak %q", want)
		}
	}
	if strings.Contains(ics, "VALUE=DATE:") {
		t.Error("wydarzenie nie powinno być całodniowe")
	}
	if all := BuildICS("Test", []Collection{{d(2026, 3, 29), "X"}}, EventTime{AllDay: true}); strings.Contains(all, "VTIMEZONE") || !strings.Contains(all, "TRIGGER:-PT5H") {
		t.Error("całodniowe: zbędna strefa albo zły alarm")
	}
}

func TestParseEventTime(t *testing.T) {
	if et, err := ParseEventTime("all-day", ""); err != nil || !et.AllDay {
		t.Errorf("all-day: %+v %v", et, err)
	}
	if et, err := ParseEventTime("05:30", "45m"); err != nil || et.Start != 5*time.Hour+30*time.Minute || et.Duration != 45*time.Minute {
		t.Errorf("05:30: %+v %v", et, err)
	}
	if formatDuration(10*time.Hour+30*time.Minute) != "10H30M" || formatDuration(30*time.Minute) != "30M" {
		t.Error("formatDuration")
	}
	for _, bad := range [][2]string{{"25:00", "1h"}, {"6", "1h"}, {"06:00", "0"}, {"06:00", "abc"}} {
		if _, err := ParseEventTime(bad[0], bad[1]); err == nil {
			t.Errorf("oczekiwano błędu dla %v", bad)
		}
	}
}
