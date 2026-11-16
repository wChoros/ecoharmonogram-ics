package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(perHour float64, burst, max int) (*RateLimiter, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	rl := NewRateLimiter(perHour, burst, max)
	rl.now = clk.now
	return rl, clk
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	rl, clk := newTestLimiter(60, 3, 10) // 1 żądanie na minutę
	ip := clientKey("203.0.113.5:1234")
	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow(ip); !ok {
			t.Fatalf("żądanie %d w burst odrzucone", i)
		}
	}
	ok, wait := rl.Allow(ip)
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("po burst: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := rl.Allow(clientKey("198.51.100.1:1")); !ok {
		t.Error("inny klient nie powinien być limitowany")
	}
	clk.advance(time.Minute)
	if ok, _ := rl.Allow(ip); !ok {
		t.Error("po minucie powinien być 1 token")
	}
	if ok, _ := rl.Allow(ip); ok {
		t.Error("drugi token nie powinien być jeszcze dostępny")
	}
}

func TestRateLimiterBoundedClients(t *testing.T) {
	rl, clk := newTestLimiter(60, 2, 3)
	for i := 1; i <= 3; i++ {
		rl.Allow(netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), 32))
	}
	if ok, _ := rl.Allow(clientKey("10.0.0.9:1")); ok {
		t.Error("pełna tabela powinna odrzucić nowego klienta")
	}
	if rl.Len() != 3 {
		t.Errorf("Len=%d", rl.Len())
	}
	clk.advance(3 * time.Minute) // kubełki się napełniły, więc można je usunąć
	if ok, _ := rl.Allow(clientKey("10.0.0.9:1")); !ok {
		t.Error("po napełnieniu kubełków nowy klient powinien wejść")
	}
	if rl.Len() != 1 {
		t.Errorf("po czyszczeniu Len=%d, oczekiwano 1", rl.Len())
	}
}

func TestClientKeyIPv6Slash64(t *testing.T) {
	a := clientKey("[2001:db8:1:2:aaaa::1]:443")
	b := clientKey("[2001:db8:1:2:bbbb::2]:443")
	c := clientKey("[2001:db8:1:3::1]:443")
	if a != b || a == c {
		t.Errorf("a=%v b=%v c=%v", a, b, c)
	}
	if got := clientKey("[::ffff:192.0.2.1]:80"); got != netip.MustParsePrefix("192.0.2.1/32") {
		t.Errorf("IPv4-mapped: %v", got)
	}
}

func TestServerHandler(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "c.ics")
	if err := os.WriteFile(file, []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{urlPath: "/tok.ics", cal: &calendarFile{path: file}, limiter: NewRateLimiter(3600, 3, 10)}
	do := func(method, path, inm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "192.0.2.7:5555"
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if r := do("GET", "/inne", ""); r.Code != http.StatusNotFound {
		t.Errorf("zła ścieżka: %d", r.Code)
	}
	r := do("GET", "/tok.ics", "")
	if r.Code != 200 || r.Header().Get("Content-Type") != "text/calendar; charset=utf-8" || r.Header().Get("ETag") == "" {
		t.Fatalf("GET: %d %v", r.Code, r.Header())
	}
	if r2 := do("GET", "/tok.ics", r.Header().Get("ETag")); r2.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", r2.Code)
	}
	if r3 := do("POST", "/tok.ics", ""); r3.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", r3.Code)
	}
	if r4 := do("GET", "/tok.ics", ""); r4.Code != http.StatusTooManyRequests || r4.Header().Get("Retry-After") == "" {
		t.Errorf("limit: %d %v", r4.Code, r4.Header())
	}
}
