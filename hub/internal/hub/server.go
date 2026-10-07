// Package hub is the hack-box session hub: session registry, archive store,
// attendee download pages, GitHub push queue and the staff dashboard.
package hub

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultMaxArchive is the upload cap for one project archive.
const DefaultMaxArchive int64 = 250 << 20

// Options are the knobs New takes. Zero values get sensible defaults.
type Options struct {
	Config  Config
	DataDir string
	Logger  *log.Logger
	// APITestPage is served at /dash/api_test.html when set.
	APITestPage []byte
	// Now, MaxArchive, Location, GitHubAPI and GitCmd exist for tests.
	Now        func() time.Time
	MaxArchive int64
	Location   *time.Location
	GitHubAPI  string // default https://api.github.com
	GitHubPush string // default https://github.com
	GitCmd     string // default "git"
}

// Server holds the hub's state. Create it with New.
type Server struct {
	cfg        Config
	db         *sql.DB
	dataDir    string
	archiveDir string
	log        *log.Logger
	now        func() time.Time
	maxArchive int64
	loc        *time.Location
	apiTest    []byte
	limiter    *rateLimiter
	// enrollLimiter caps POST /api/v1/enroll per client address.
	enrollLimiter *rateLimiter
	boxIPs        *boxIPSet
	gh            *githubWorker
	// pay is the partner signer wallet (nil when credits are off); payMu serialises claims
	// so the per-wallet cap cannot be overrun by two phones at once.
	pay        *paySigner
	payHTTP    *http.Client
	payLimiter *rateLimiter
	payMu      sync.Mutex
	// faucet funding (faucet.go): fundMu serialises POST /d/{token}/fund so the caps hold.
	faucetHTTP      *http.Client
	fundLimiter     *rateLimiter
	fundReadLimiter *rateLimiter
	fundMu          sync.Mutex
}

// New opens the database under DataDir and returns a ready Server.
func New(o Options) (*Server, error) {
	if err := o.Config.normalize(); err != nil {
		return nil, err
	}
	if o.DataDir == "" {
		return nil, errors.New("data dir is required")
	}
	s := &Server{
		cfg:        o.Config,
		dataDir:    o.DataDir,
		archiveDir: filepath.Join(o.DataDir, "archives"),
		log:        o.Logger,
		now:        o.Now,
		maxArchive: o.MaxArchive,
		loc:        o.Location,
		apiTest:    o.APITestPage,
	}
	if s.log == nil {
		s.log = log.New(os.Stderr, "", log.LstdFlags)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.maxArchive <= 0 {
		s.maxArchive = DefaultMaxArchive
	}
	if s.loc == nil {
		s.loc = time.Local
	}
	if err := os.MkdirAll(s.archiveDir, 0o700); err != nil {
		return nil, err
	}
	db, err := openDB(o.DataDir)
	if err != nil {
		return nil, err
	}
	s.db = db
	s.limiter = newRateLimiter(5, time.Minute, s.now)
	s.enrollLimiter = newRateLimiter(10, time.Minute, s.now)
	s.boxIPs = newBoxIPSet(s.now)
	s.payLimiter = newRateLimiter(10, time.Minute, s.now)
	proxyKey = s.cfg.ProxyKey
	s.payHTTP = &http.Client{Timeout: 35 * time.Second}
	if s.cfg.Pay.enabled() {
		s.pay, _ = newPaySigner(s.cfg.Pay.SignerKey) // validated by normalize
		s.log.Printf("pay: compute credits on, %s per session, %d per wallet, signer %s",
			s.cfg.Pay.amountUSD(), s.cfg.Pay.MaxPerWallet, s.pay.address)
	}
	s.faucetHTTP = &http.Client{Timeout: 20 * time.Second}
	s.fundLimiter = newRateLimiter(s.cfg.Faucet.IPPerMinute, time.Minute, s.now)
	s.fundReadLimiter = newRateLimiter(10*max(s.cfg.Faucet.IPPerMinute, 30), time.Minute, s.now)
	if f := s.cfg.Faucet; f.enabled() {
		mode := "fixed drip"
		if f.PromoCode != "" {
			mode = "promo code"
		}
		s.log.Printf("faucet: funding on via %s (%s), %s 0G per transfer, caps %s per session, %s per wallet, %s per box hour, %s per event",
			f.APIURL, mode, fmtOG(milli(f.AmountOG)), fmtOG(milli(f.SessionMaxOG)), fmtOG(milli(f.WalletMaxOG)),
			fmtOG(milli(f.BoxHourlyMaxOG)), fmtOG(milli(f.EventBudgetOG)))
	}
	s.gh = newGitHubWorker(s, o.GitHubAPI, o.GitHubPush, o.GitCmd)
	if err := s.gh.requeueDisabled(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Server) Close() error { return s.db.Close() }

// RunWorker runs the GitHub push worker until ctx ends.
func (s *Server) RunWorker(ctx context.Context) { s.gh.run(ctx) }

// Handler returns the full HTTP handler with logging and the tunnel guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Box API (bearer token).
	mux.HandleFunc("POST /api/v1/sessions", s.boxAuth(s.apiCreateSession))
	mux.HandleFunc("GET /api/v1/sessions/{id}/qr.png", s.boxAuth(s.apiQR))
	mux.HandleFunc("POST /api/v1/heartbeat", s.boxAuth(s.apiHeartbeat))
	mux.HandleFunc("POST /api/v1/commands/{id}/result", s.boxAuth(s.apiCommandResult))
	mux.HandleFunc("POST /api/v1/sessions/{id}/end", s.boxAuth(s.apiEnd))
	mux.HandleFunc("PUT /api/v1/sessions/{id}/archive", s.boxAuth(s.apiArchive))
	mux.HandleFunc("GET /api/v1/config", s.boxAuth(s.apiConfig))
	mux.HandleFunc("POST /api/v1/enroll", s.apiEnroll)

	// Public pages (the only paths the tunnel should carry).
	mux.HandleFunc("GET /d/{token}", s.pageDownload)
	mux.HandleFunc("GET /d/{token}/download", s.pageDownloadZip)
	mux.HandleFunc("POST /d/{token}/credit", s.pageCredit)
	mux.HandleFunc("POST /d/{token}/fund", s.pageFund)
	mux.HandleFunc("GET /d/{token}/fund", s.pageFundStatus)
	mux.HandleFunc("GET /code", s.pageCode)
	mux.HandleFunc("GET /c/{code}", s.pageShort)
	mux.HandleFunc("POST /code", s.pageCodePost)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok")
	})

	// Dashboard (tailnet only).
	mux.HandleFunc("GET /{$}", s.dashPage)
	mux.HandleFunc("GET /dash/state", s.dashState)
	mux.HandleFunc("POST /dash/boxes/{box}/extend", s.dashGuard(s.dashExtend))
	mux.HandleFunc("POST /dash/boxes/{box}/end", s.dashGuard(s.dashEnd))
	mux.HandleFunc("POST /dash/boxes/{box}/finish", s.dashGuard(s.dashFinish))
	mux.HandleFunc("POST /dash/requests/{id}", s.dashGuard(s.dashDecide))
	mux.HandleFunc("POST /dash/boxes/{box}/release", s.dashGuard(s.dashRelease))
	mux.HandleFunc("GET /dash/sessions/{id}/download", s.dashDownload)
	mux.HandleFunc("GET /dash/fundings.csv", s.dashFundingsCSV)
	mux.HandleFunc("GET /dash/rewards.csv", s.dashRewardsCSV)
	mux.HandleFunc("GET /dash/api_test.html", s.dashAPITest)
	mux.HandleFunc("GET /dash/config", s.dashConfigGet)
	mux.HandleFunc("POST /dash/config", s.dashGuard(s.dashConfigSet))

	return s.logRequests(tunnelGuard(s.dashboardGuard(mux)))
}

// tunnelGuard refuses the dashboard and the box API to anything that came
// through Cloudflare, so a tunnel misconfiguration cannot publish them.
func tunnelGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cf-Connecting-IP") != "" && isPrivatePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPrivatePath(p string) bool {
	return p == "/" || p == "" || p == "/dash" || p == "/api" ||
		strings.HasPrefix(p, "/dash/") || strings.HasPrefix(p, "/api/")
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	bytes int64
}

func (w *statusWriter) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
	w.ResponseWriter.WriteHeader(c)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/dash/state" && sw.code == 200 {
			return // polled every 2 s by every open dashboard; too noisy
		}
		s.log.Printf("%s %s %s %d %dB %s", clientIP(r), r.Method, logPath(r.URL.Path), sw.code, sw.bytes,
			time.Since(start).Round(time.Millisecond))
	})
}

// logPath shortens download tokens so the log does not hold working links.
func logPath(p string) string {
	if strings.HasPrefix(p, "/d/") {
		rest := p[3:]
		tok, tail, _ := strings.Cut(rest, "/")
		if len(tok) > 6 {
			tok = tok[:6] + "..."
		}
		if tail != "" {
			return "/d/" + tok + "/" + tail
		}
		return "/d/" + tok
	}
	return p
}

// boxAuth maps the bearer token to a box name, or answers 401. Config tokens
// and enrolled tokens count; the enroll token does not.
func (s *Server) boxAuth(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(auth, "Bearer ")
		box := ""
		if ok && tok != "" {
			for t, name := range s.cfg.Boxes {
				if subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 {
					box = name
				}
			}
		}
		if box == "" && ok && tok != "" {
			box = s.enrolledBox(tok)
		}
		if box == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hackbox-hub"`)
			jsonError(w, http.StatusUnauthorized, "unknown box token")
			return
		}
		s.boxIPs.see(r)
		h(w, r, box)
	}
}

// dashGuard blocks cross-site form posts to the dashboard actions: the call
// must be JSON, and a browser Origin, when sent, must match the Host.
func (s *Server) dashGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			jsonError(w, http.StatusUnsupportedMediaType, "send Content-Type: application/json")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				jsonError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// readJSON decodes a small JSON body. An empty body leaves v untouched.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "body too large")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		jsonError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

// proxyKey is Config.ProxyKey; set by New (one hub per process).
var proxyKey string

func clientIP(r *http.Request) string {
	if proxyKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Hackbox-Proxy-Key")), []byte(proxyKey)) == 1 {
		if ip := strings.TrimSpace(r.Header.Get("X-Hackbox-Client-IP")); ip != "" {
			return ip
		}
	}
	if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// randomToken returns 32 lowercase base32 characters (160 random bits).
func randomToken() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(b32.EncodeToString(b))
}

// randomID returns a short opaque session id.
func randomID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(b32.EncodeToString(b))
}

// CodeAlphabet is the pickup code alphabet: no 0/O/1/I.
const CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomCode() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = CodeAlphabet[int(b[i])%len(CodeAlphabet)] // 256 % 32 == 0, no bias
	}
	return string(b)
}

func normalizeCode(c string) string {
	c = strings.ToUpper(c)
	var sb strings.Builder
	for _, r := range c {
		if strings.ContainsRune(CodeAlphabet, r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// rateLimiter is a sliding window counter per key.
type rateLimiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

func newRateLimiter(n int, window time.Duration, now func() time.Time) *rateLimiter {
	return &rateLimiter{n: n, window: window, now: now, hits: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-l.window)
	if len(l.hits) > 10000 {
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	h := l.hits[key]
	i := 0
	for i < len(h) && !h[i].After(cut) {
		i++
	}
	h = h[i:]
	if len(h) >= l.n {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

// cleanText keeps printable characters and trims to max runes.
func cleanText(s string, max int) string {
	var sb strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue
		}
		if n >= max {
			break
		}
		sb.WriteRune(r)
		n++
	}
	return strings.TrimSpace(sb.String())
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
