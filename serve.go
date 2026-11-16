package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// calendarFile trzyma plik w pamięci i wczytuje go ponownie, gdy cron go podmieni.
type calendarFile struct {
	path string

	mu      sync.Mutex
	data    []byte
	modTime time.Time
	size    int64
	etag    string
}

func (c *calendarFile) get() ([]byte, time.Time, string, error) {
	st, err := os.Stat(c.path)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil || !st.ModTime().Equal(c.modTime) || st.Size() != c.size {
		data, err := os.ReadFile(c.path)
		if err != nil {
			return nil, time.Time{}, "", err
		}
		c.data, c.modTime, c.size = data, st.ModTime(), st.Size()
		c.etag = fmt.Sprintf(`"%08x-%x"`, crc32.ChecksumIEEE(data), len(data))
	}
	return c.data, c.modTime, c.etag, nil
}

type server struct {
	urlPath string
	cal     *calendarFile
	limiter *RateLimiter

	served, notModified, limited, notFound atomic.Int64
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "ecocal")
	if r.URL.Path != s.urlPath {
		s.notFound.Add(1)
		http.NotFound(w, r)
		return
	}
	if ok, wait := s.limiter.Allow(clientKey(r.RemoteAddr)); !ok {
		s.limited.Add(1)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, mod, etag, err := s.cal.get()
	if err != nil {
		log.Printf("odczyt kalendarza: %v", err)
		http.Error(w, "calendar unavailable", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	rec := &statusRecorder{ResponseWriter: w}
	http.ServeContent(rec, r, "calendar.ics", mod, bytes.NewReader(data))
	if rec.status == http.StatusNotModified {
		s.notModified.Add(1)
	} else {
		s.served.Add(1)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func runServe(addr, file, urlPath string, perHour float64, burst int) error {
	if len(urlPath) < 2 || urlPath[0] != '/' {
		return errors.New("-path musi zaczynać się od / (np. /<token>.ics)")
	}
	if perHour <= 0 || burst < 1 {
		return errors.New("-rate musi być > 0, a -burst ≥ 1")
	}
	s := &server{
		urlPath: urlPath,
		cal:     &calendarFile{path: file},
		limiter: NewRateLimiter(perHour, burst, 4096),
	}
	if _, _, _, err := s.cal.get(); err != nil {
		log.Printf("uwaga: brak pliku kalendarza (%v), serwuję 503 do czasu pierwszego fetch", err)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(os.Stderr, "http: ", 0),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		prune := time.NewTicker(time.Minute)
		report := time.NewTicker(time.Hour)
		defer prune.Stop()
		defer report.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-prune.C:
				s.limiter.Prune()
			case <-report.C:
				ok, nm, lim, nf := s.served.Swap(0), s.notModified.Swap(0), s.limited.Swap(0), s.notFound.Swap(0)
				if ok+nm+lim+nf > 0 {
					log.Printf("ostatnia godzina: 200=%d 304=%d 429=%d 404=%d klienci=%d", ok, nm, lim, nf, s.limiter.Len())
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("nasłuchuję na %s (limit %.0f/h, burst %d)", addr, perHour, burst)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
