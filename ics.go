package main

import (
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
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

// BuildICS składa deterministyczny kalendarz: te same dane dają identyczne bajty,
// więc plik (i ETag) zmienia się tylko wtedy, gdy zmieni się harmonogram.
func BuildICS(name string, cs []Collection) string {
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
		line("DTSTART;VALUE=DATE:" + c.Date.Format("20060102"))
		line("DTEND;VALUE=DATE:" + c.Date.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + escapeText(Summary(c.Kind)))
		line("DESCRIPTION:" + escapeText("Wystaw pojemniki/worki do 6:00 rano w dniu odbioru albo wieczorem dzień wcześniej."))
		line("TRANSP:TRANSPARENT")
		line("BEGIN:VALARM")
		line("ACTION:DISPLAY")
		line("DESCRIPTION:" + escapeText("Jutro: "+Summary(c.Kind)))
		line("TRIGGER:-PT5H") // 19:00 dzień wcześniej
		line("END:VALARM")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
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
