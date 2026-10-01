package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Enrollment: one USB stick for the whole fleet. A box presents the fleet
// enroll token and its MAC, and gets a name (<prefix>N) and its own token.
// The same MAC always gets the same name back.

var macRe = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// releaseQuiet: a name can be released only after this long without a heartbeat.
const releaseQuiet = 5 * 60

// boxIPTTL: how long an address that made a box API call is kept off the dashboard.
const boxIPTTL = 24 * time.Hour

// enrolledToken returns 40 lowercase base32 characters (200 random bits).
func enrolledToken() string {
	b := make([]byte, 25)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(b32.EncodeToString(b))
}

type enrollReq struct {
	MAC             string `json:"mac"`
	CurrentHostname string `json:"current_hostname"`
}

type enrollResp struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

func (s *Server) apiEnroll(w http.ResponseWriter, r *http.Request) {
	if !s.enrollLimiter.allow(clientIP(r)) {
		jsonError(w, http.StatusTooManyRequests, "too many enroll attempts, wait a minute")
		return
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	et := s.cfg.EnrollToken
	if !ok || et == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(et)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hackbox-hub"`)
		jsonError(w, http.StatusUnauthorized, "unknown enroll token")
		return
	}
	s.boxIPs.see(r)
	var req enrollReq
	if !readJSON(w, r, &req) {
		return
	}
	mac := strings.ToLower(strings.TrimSpace(req.MAC))
	if !macRe.MatchString(mac) || mac == "00:00:00:00:00:00" {
		jsonError(w, http.StatusBadRequest, "mac must be 6 hex pairs like aa:bb:cc:dd:ee:ff")
		return
	}
	host := cleanText(req.CurrentHostname, 64)
	now := s.now().Unix()
	token := enrolledToken()

	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer tx.Rollback()
	var name string
	err = tx.QueryRow(`SELECT name FROM enrollments WHERE mac = ?`, mac).Scan(&name)
	switch {
	case err == nil:
		// Reinstall: same name, new token; the old one stops working.
		_, err = tx.Exec(`UPDATE enrollments SET token = ?, enrolled_at = ?, current_hostname = ? WHERE mac = ?`,
			token, now, host, mac)
	case errors.Is(err, sql.ErrNoRows):
		name, err = s.freeName(tx)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO enrollments (mac, name, token, enrolled_at, current_hostname) VALUES (?,?,?,?,?)`,
				mac, name, token, now, host)
		}
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, err)
		return
	}
	s.log.Printf("enroll: %s -> %s", mac, name)
	writeJSON(w, http.StatusOK, enrollResp{Name: name, Token: token})
}

// freeName is the lowest <prefix>N not bound to a MAC and never seen in a
// heartbeat. Names that exist only as unused config tokens count as free.
func (s *Server) freeName(tx *sql.Tx) (string, error) {
	for n := 1; n <= 10000; n++ {
		name := fmt.Sprintf("%s%d", s.cfg.NamePrefix, n)
		var taken int
		err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM enrollments WHERE name = ?) +
			(SELECT COUNT(*) FROM boxes WHERE name = ? AND last_seen > 0)`, name, name).Scan(&taken)
		if err != nil {
			return "", err
		}
		if taken == 0 {
			return name, nil
		}
	}
	return "", errors.New("no free box name")
}

// enrolledBox maps an enrolled token to its box name, or "".
func (s *Server) enrolledBox(tok string) string {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM enrollments WHERE token = ?`, tok).Scan(&name); err != nil {
		return ""
	}
	return name
}

type enrollment struct {
	MAC      string
	Hostname string
	At       int64
}

func (s *Server) enrollments() (map[string]enrollment, error) {
	rows, err := s.db.Query(`SELECT name, mac, current_hostname, enrolled_at FROM enrollments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]enrollment{}
	for rows.Next() {
		var n string
		var e enrollment
		if err := rows.Scan(&n, &e.MAC, &e.Hostname, &e.At); err != nil {
			return nil, err
		}
		out[n] = e
	}
	return out, rows.Err()
}

// allBoxNames: configured boxes plus enrolled ones, naturally sorted.
func (s *Server) allBoxNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range s.cfg.boxNames() {
		add(n)
	}
	if en, err := s.enrollments(); err == nil {
		for n := range en {
			add(n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i], out[j]) })
	return out
}

// dashRelease unbinds a dead box's name: the MAC binding and the enrolled
// token go, and so does its heartbeat record, so the name is free again.
func (s *Server) dashRelease(w http.ResponseWriter, r *http.Request) {
	box := r.PathValue("box")
	var lastSeen int64
	hasRow := true
	if err := s.db.QueryRow(`SELECT last_seen FROM boxes WHERE name = ?`, box).Scan(&lastSeen); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.serverError(w, err)
			return
		}
		hasRow = false
	}
	var mac string
	enrolled := s.db.QueryRow(`SELECT mac FROM enrollments WHERE name = ?`, box).Scan(&mac) == nil
	if !hasRow && !enrolled {
		jsonError(w, http.StatusNotFound, "no such box name in use")
		return
	}
	if s.now().Unix()-lastSeen < releaseQuiet {
		jsonError(w, http.StatusConflict, box+" sent a heartbeat in the last 5 minutes; switch it off first")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM enrollments WHERE name = ?`,
		`DELETE FROM boxes WHERE name = ?`,
		`DELETE FROM commands WHERE box = ? AND picked_at = 0`,
	} {
		if _, err := tx.Exec(q, box); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, err)
		return
	}
	if enrolled {
		s.log.Printf("release: %s (was %s), enrolled token revoked", box, mac)
	} else {
		s.log.Printf("release: %s", box)
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": box, "revoked": enrolled})
}

// cleanActivity keeps up to 12 valid entries: strings of 1..32 printable
// characters. Anything else is dropped.
func cleanActivity(raw json.RawMessage) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return out
	}
	for _, it := range items {
		str, ok := it.(string)
		if !ok {
			continue
		}
		str = strings.TrimSpace(str)
		n := utf8.RuneCountInString(str)
		if n < 1 || n > 32 {
			continue
		}
		good := true
		for _, r := range str {
			if !unicode.IsPrint(r) {
				good = false
				break
			}
		}
		if good {
			out = append(out, str)
		}
		if len(out) == 12 {
			break
		}
	}
	return out
}

// boxIPSet remembers addresses that made authenticated box API calls, so the
// dashboard can refuse them: a box shares the hub port with the dashboard.
type boxIPSet struct {
	mu  sync.Mutex
	ips map[string]time.Time
	now func() time.Time
}

func newBoxIPSet(now func() time.Time) *boxIPSet {
	return &boxIPSet{ips: map[string]time.Time{}, now: now}
}

// remoteHost is the TCP peer. Cf-Connecting-IP is not trusted here; requests
// carrying it never reach the box API or the dashboard anyway.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// see records the caller. Loopback is never recorded: the hub host itself
// (local tests, a local reverse proxy) must keep its dashboard.
func (b *boxIPSet) see(r *http.Request) {
	h := remoteHost(r)
	if isLoopback(h) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if len(b.ips) > 4096 {
		for k, t := range b.ips {
			if now.Sub(t) > boxIPTTL {
				delete(b.ips, k)
			}
		}
	}
	b.ips[h] = now
}

func (b *boxIPSet) isBox(r *http.Request) bool {
	h := remoteHost(r)
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.ips[h]
	return ok && b.now().Sub(t) <= boxIPTTL
}

// dashboardGuard refuses the dashboard to addresses that act as boxes.
func (s *Server) dashboardGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if (p == "/" || p == "/dash" || strings.HasPrefix(p, "/dash/")) && s.boxIPs.isBox(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintln(w, "The dashboard is not available from a hack box.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
