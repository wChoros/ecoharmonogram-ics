package main

import (
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var kindEmoji = map[string]string{
	"ODPADY NIESEGREGOWANE":            "⚫",
	"ODPADY ZMIESZANE":                 "⚫",
	"PAPIER, TWORZYWA SZTUCZNE, SZKŁO": "🟡",
	"PAPIER":                           "🔵",
	"TWORZYWA SZTUCZNE":                "🟡",
	"METALE I TWORZYWA SZTUCZNE":       "🟡",
	"SZKŁO":                            "🟢",
	"ODPADY BIODEGRADOWALNE":           "🟤",
	"BIO":                              "🟤",
	"ODPADY WIELKOGABARYTOWE":          "🟣",
	"CHOINKI BOŻONARODZENIOWE":         "🎄",
	"TERMINY PŁATNOŚCI":                "💰",
}

// PaymentsKind to frakcja z terminami opłat, domyślnie pomijana.
const PaymentsKind = "TERMINY PŁATNOŚCI"

// EventTime określa porę wydarzenia względem dnia odbioru.
type EventTime struct {
	AllDay     bool
	Start      time.Duration // od północy czasu polskiego, np. 21h
	Duration   time.Duration
	DaysBefore int  // 1 = wydarzenie dzień przed odbiorem
	Alarm      bool // alarm w chwili rozpoczęcia wydarzenia
}

// ParseEventTime czyta "06:00" + "1h"; "all-day" (albo "") daje wydarzenia całodniowe.
func ParseEventTime(start, duration string) (EventTime, error) {
	if start == "" || strings.EqualFold(start, "all-day") {
		return EventTime{AllDay: true}, nil
	}
	t, err := time.Parse("15:04", start)
	if err != nil {
		return EventTime{}, fmt.Errorf("zła godzina %q (format HH:MM albo all-day)", start)
	}
	d, err := time.ParseDuration(duration)
	if err != nil || d <= 0 || d > 24*time.Hour {
		return EventTime{}, fmt.Errorf("zły czas trwania %q (np. 1h, 30m)", duration)
	}
	return EventTime{Start: time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, Duration: d}, nil
}

// Strefa Europe/Warsaw zapisana w pliku, żeby nie zależeć od bazy tzdata klienta.
var vtimezoneWarsaw = []string{
	"BEGIN:VTIMEZONE",
	"TZID:Europe/Warsaw",
	"BEGIN:DAYLIGHT",
	"TZOFFSETFROM:+0100",
	"TZOFFSETTO:+0200",
	"TZNAME:CEST",
	"DTSTART:19700329T020000",
	"RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU",
	"END:DAYLIGHT",
	"BEGIN:STANDARD",
	"TZOFFSETFROM:+0200",
	"TZOFFSETTO:+0100",
	"TZNAME:CET",
	"DTSTART:19701025T030000",
	"RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU",
	"END:STANDARD",
	"END:VTIMEZONE",
}

// BuildICS składa deterministyczny kalendarz: te same dane dają identyczne bajty,
// więc plik (i ETag) zmienia się tylko wtedy, gdy zmieni się harmonogram.
func BuildICS(name string, cs []Collection, et EventTime) string {
	sorted := append([]Collection(nil), cs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Date.Equal(sorted[j].Date) {
			return sorted[i].Date.Before(sorted[j].Date)
		}
		return sorted[i].Kind < sorted[j].Kind
	})

	var b strings.Builder
	line := func(s string) { b.WriteString(fold(s)) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//ecoharmonogram-ics//PL")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + escapeText(name))
	line("X-WR-TIMEZONE:Europe/Warsaw")
	line("REFRESH-INTERVAL;VALUE=DURATION:PT12H")
	line("X-PUBLISHED-TTL:PT12H")
	if !et.AllDay {
		for _, l := range vtimezoneWarsaw {
			line(l)
		}
	}
	seen := make(map[string]bool)
	for _, c := range sorted {
		uid := fmt.Sprintf("%s-%08x@ecocal", c.Date.Format("20060102"), crc32.ChecksumIEEE([]byte(c.Kind)))
		if seen[uid] {
			continue
		}
		seen[uid] = true
		line("BEGIN:VEVENT")
		line("UID:" + uid)
		line("DTSTAMP:" + c.Date.Format("20060102") + "T000000Z")
		day := c.Date.AddDate(0, 0, -et.DaysBefore)
		if et.AllDay {
			line("DTSTART;VALUE=DATE:" + day.Format("20060102"))
			line("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102"))
		} else {
			// Czas lokalny bez strefy w time.Time; strefę niesie TZID.
			start := day.Add(et.Start)
			line("DTSTART;TZID=Europe/Warsaw:" + start.Format("20060102T150405"))
			line("DTEND;TZID=Europe/Warsaw:" + start.Add(et.Duration).Format("20060102T150405"))
		}
		summary := SummaryFor(c.Kind, et.DaysBefore)
		line("SUMMARY:" + escapeText(summary))
		line("DESCRIPTION:" + escapeText("Odbiór "+c.Date.Format("02.01.2006")+". Wystaw pojemniki/worki do 6:00 rano w dniu odbioru albo wieczorem dzień wcześniej."))
		line("TRANSP:TRANSPARENT")
		if et.Alarm {
			line("BEGIN:VALARM")
			line("ACTION:DISPLAY")
			line("DESCRIPTION:" + escapeText(summary))
			line("TRIGGER:PT0S")
			line("END:VALARM")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

// SummaryFor dodaje "Jutro:" / "Za N dni:", gdy wydarzenie jest przed dniem odbioru:
// "⚫ Jutro: odpady niesegregowane".
func SummaryFor(kind string, daysBefore int) string {
	s := Summary(kind)
	if daysBefore <= 0 {
		return s
	}
	emoji, text, _ := strings.Cut(s, " ")
	prefix := "Jutro"
	if daysBefore > 1 {
		prefix = fmt.Sprintf("Za %d dni", daysBefore)
	}
	r := []rune(text)
	r[0] = unicode.ToLower(r[0])
	return emoji + " " + prefix + ": " + string(r)
}

// Summary zamienia "ODPADY NIESEGREGOWANE" na "⚫ Odpady niesegregowane".
func Summary(kind string) string {
	lower := []rune(strings.ToLower(kind))
	if len(lower) > 0 {
		lower[0] = unicode.ToUpper(lower[0])
	}
	emoji, ok := kindEmoji[strings.ToUpper(kind)]
	if !ok {
		emoji = "🗑️"
	}
	return emoji + " " + string(lower)
}

// escapeText escapuje wartość typu TEXT wg RFC 5545 §3.3.11.
func escapeText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// fold łamie linię na kawałki ≤75 oktetów (RFC 5545 §3.1), nie rozcinając znaków UTF-8,
// i kończy ją CRLF.
func fold(s string) string {
	const limit = 75
	if len(s) <= limit {
		return s + "\r\n"
	}
	var b strings.Builder
	max := limit
	for len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		max = limit - 1 // linia kontynuacji zaczyna się spacją
	}
	b.WriteString(s)
	b.WriteString("\r\n")
	return b.String()
}
