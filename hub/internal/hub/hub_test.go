package hub

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	tok1 = "devtoken-box-one"
	tok2 = "devtoken-box-two"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	clk *clock
	log *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.b.Write(p) }
func (b *syncBuf) String() string              { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 1, 14, 5, 0, 0, time.UTC)}
	lb := &syncBuf{}
	o := Options{
		Config: Config{
			PublicBaseURL: "https://hackbox.example.dev/",
			Boxes:         map[string]string{tok1: "hackbox1", tok2: "hackbox2"},
			GitHub:        GitHubConfig{Org: "0g-hackbox-sessions"},
			DownloadDays:  7,
		},
		DataDir:  t.TempDir(),
		Logger:   log.New(lb, "", 0),
		Now:      clk.Now,
		Location: time.UTC,
	}
	if mod != nil {
		mod(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &env{t: t, s: s, h: s.Handler(), clk: clk, log: lb}
}

type hdr map[string]string

func (e *env) do(method, path string, body any, h hdr) *httptest.ResponseRecorder {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	case string:
		r = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, r)
	// Boxes call from 192.0.2.10; staff use the dashboard from 192.0.2.50, since
	// the hub refuses the dashboard to addresses that made box API calls.
	req.RemoteAddr = "192.0.2.10:5555"
	if isPrivatePath(path) && !strings.HasPrefix(path, "/api/") {
		req.RemoteAddr = "192.0.2.50:5555"
	}
	if ra, ok := h["X-Test-Remote"]; ok {
		req.RemoteAddr = ra
	}
	if body != nil {
		if _, raw := body.([]byte); !raw {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for k, v := range h {
		if k == "X-Test-Remote" {
			continue
		}
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

func box(tok string) hdr { return hdr{"Authorization": "Bearer " + tok} }

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func (e *env) create(tok, name string) createSessionResp {
	e.t.Helper()
	w := e.do("POST", "/api/v1/sessions", map[string]any{
		"name": name, "agent": "claude", "minutes": 30, "started_at": e.clk.Now().Unix(), "local_code": "",
	}, box(tok))
	if w.Code != 200 {
		e.t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	return decode[createSessionResp](e.t, w)
}

type tarEntry struct {
	name string
	typ  byte
	body string
	link string
	mode int64
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, en := range entries {
		h := &tar.Header{Name: en.name, Typeflag: en.typ, Mode: en.mode, Linkname: en.link, ModTime: time.Unix(1790000000, 0)}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if en.typ == tar.TypeReg {
			h.Size = int64(len(en.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if en.typ == tar.TypeReg {
			tw.Write([]byte(en.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

var goodTar = []tarEntry{
	{name: "project/", typ: tar.TypeDir, mode: 0o755},
	{name: "project/README.md", typ: tar.TypeReg, body: "# hello\n"},
	{name: "project/src/", typ: tar.TypeDir, mode: 0o755},
	{name: "project/src/main.py", typ: tar.TypeReg, body: "print('hi')\n", mode: 0o755},
}

var evilTar = []tarEntry{
	{name: "project/ok.txt", typ: tar.TypeReg, body: "fine"},
	{name: "project/link", typ: tar.TypeSymlink, link: "/etc/passwd"},
	{name: "project/hard", typ: tar.TypeLink, link: "project/ok.txt"},
	{name: "project/../../escape.txt", typ: tar.TypeReg, body: "evil"},
	{name: "../outside.txt", typ: tar.TypeReg, body: "evil"},
	{name: "/etc/abs.txt", typ: tar.TypeReg, body: "evil"},
	{name: "project/dev", typ: tar.TypeChar},
	{name: "project/fifo", typ: tar.TypeFifo},
	{name: "project/.git/config", typ: tar.TypeReg, body: "[core]\n\tfsmonitor = touch /tmp/pwned\n"},
}

func zipNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(c)
	}
	return out
}

// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/healthz", nil, nil)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body)
	}
	// Allowed through the tunnel too.
	if w := e.do("GET", "/healthz", nil, hdr{"Cf-Connecting-IP": "203.0.113.9"}); w.Code != 200 {
		t.Fatalf("healthz via tunnel: %d", w.Code)
	}
}

func TestBoxAuthFailures(t *testing.T) {
	e := newEnv(t, nil)
	calls := []struct{ m, p string }{
		{"POST", "/api/v1/sessions"},
		{"GET", "/api/v1/sessions/x/qr.png"},
		{"POST", "/api/v1/heartbeat"},
		{"POST", "/api/v1/commands/1/result"},
		{"POST", "/api/v1/sessions/x/end"},
		{"PUT", "/api/v1/sessions/x/archive"},
	}
	for _, c := range calls {
		for _, h := range []hdr{nil, box("wrong-token-xyz"), {"Authorization": tok1}, {"Authorization": "Bearer "}} {
			if w := e.do(c.m, c.p, "{}", h); w.Code != 401 {
				t.Errorf("%s %s with %v: got %d, want 401", c.m, c.p, h, w.Code)
			}
		}
	}
}

func TestTunnelGuard(t *testing.T) {
	e := newEnv(t, nil)
	cf := hdr{"Cf-Connecting-IP": "203.0.113.9", "Authorization": "Bearer " + tok1}
	for _, p := range []string{"/", "/dash/state", "/dash/api_test.html", "/dash/sessions/x/download", "/api/v1/heartbeat", "/api/v1/sessions"} {
		for _, m := range []string{"GET", "POST"} {
			if w := e.do(m, p, "{}", cf); w.Code != 404 {
				t.Errorf("%s %s via tunnel: got %d, want 404", m, p, w.Code)
			}
		}
	}
	// The same calls without the header work.
	if w := e.do("GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Hack-box dashboard") {
		t.Errorf("dashboard: %d", w.Code)
	}
	if w := e.do("GET", "/dash/state", nil, nil); w.Code != 200 {
		t.Errorf("state: %d", w.Code)
	}
	// Public pages pass the guard.
	if w := e.do("GET", "/code", nil, cf); w.Code != 200 {
		t.Errorf("/code via tunnel: %d", w.Code)
	}
	if w := e.do("GET", "/d/nosuchtoken", nil, cf); w.Code != 404 || !strings.Contains(w.Body.String(), "could not find") {
		t.Errorf("/d via tunnel: %d", w.Code)
	}
}

func TestCreateSession(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("POST", "/api/v1/sessions", map[string]any{
		"name": "Ada\x07 Lovelace", "agent": "claude", "minutes": 30, "started_at": e.clk.Now().Unix(), "local_code": "QWERTY",
	}, box(tok1))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := decode[createSessionResp](t, w)
	if !regexp.MustCompile(`^[a-z2-7]{32}$`).MatchString(r.Token) {
		t.Errorf("token %q", r.Token)
	}
	if !regexp.MustCompile(`^[` + CodeAlphabet + `]{6}$`).MatchString(r.Code) {
		t.Errorf("code %q", r.Code)
	}
	if r.URL != "https://hackbox.example.dev/d/"+r.Token {
		t.Errorf("url %q", r.URL)
	}
	if want := e.clk.Now().Unix() + 7*86400; r.ExpiresAt != want {
		t.Errorf("expires_at %d, want %d", r.ExpiresAt, want)
	}
	ss, err := e.s.sessionBy("id", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ss.Box != "hackbox1" || ss.Name != "Ada Lovelace" || ss.LocalCode != "QWERTY" || ss.GitHubStatus != "disabled" {
		t.Errorf("stored %+v", ss)
	}
	// Many sessions get distinct codes and tokens.
	codes := map[string]bool{r.Code: true}
	for i := 0; i < 30; i++ {
		c := e.create(tok2, "x")
		if codes[c.Code] {
			t.Fatalf("duplicate code %s", c.Code)
		}
		codes[c.Code] = true
	}
	if w := e.do("POST", "/api/v1/sessions", "{not json", box(tok1)); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
}

func TestQR(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	w := e.do("GET", "/api/v1/sessions/"+r.ID+"/qr.png", nil, box(tok1))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("qr: %d %s", w.Code, w.Header())
	}
	if w := e.do("GET", "/api/v1/sessions/"+r.ID+"/qr.png", nil, box(tok2)); w.Code != 404 {
		t.Errorf("other box qr: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/sessions/nope/qr.png", nil, box(tok1)); w.Code != 404 {
		t.Errorf("unknown qr: %d", w.Code)
	}
}

func heartbeat(e *env, tok string, body map[string]any) heartbeatResp {
	e.t.Helper()
	w := e.do("POST", "/api/v1/heartbeat", body, box(tok))
	if w.Code != 200 {
		e.t.Fatalf("heartbeat: %d %s", w.Code, w.Body)
	}
	return decode[heartbeatResp](e.t, w)
}

func dashState(e *env) dashStateResp {
	e.t.Helper()
	w := e.do("GET", "/dash/state", nil, nil)
	if w.Code != 200 {
		e.t.Fatalf("state: %d", w.Code)
	}
	return decode[dashStateResp](e.t, w)
}

func findBox(st dashStateResp, name string) dashBox {
	for _, b := range st.Boxes {
		if b.Name == name {
			return b
		}
	}
	return dashBox{}
}

func TestHeartbeatAndCommands(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada Lovelace")

	hb := heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "seconds_left": 1700, "extend_request_at": 0, "status": map[string]any{"state": "active", "disk": 3}})
	if len(hb.Commands) != 0 || hb.Extend.Status != "none" {
		t.Fatalf("first heartbeat %+v", hb)
	}
	b := findBox(dashState(e), "hackbox1")
	if !b.Online || b.State != "active" || b.SecondsLeft != 1700 || b.SessionID != r.ID || b.Attendee != "Ada Lovelace" || b.Agent != "claude" {
		t.Fatalf("box %+v", b)
	}
	if !strings.Contains(string(b.Status), `"disk":3`) {
		t.Errorf("status json %s", b.Status)
	}
	if b2 := findBox(dashState(e), "hackbox2"); b2.Online || b2.Name != "hackbox2" {
		t.Errorf("unseen box should be listed offline: %+v", b2)
	}

	// Staff extend and end.
	for _, c := range []struct {
		path string
		body any
		code int
	}{
		{"/dash/boxes/hackbox1/extend", map[string]int{"minutes": 10}, 200},
		{"/dash/boxes/hackbox1/extend", map[string]int{"minutes": 0}, 400},
		{"/dash/boxes/hackbox1/extend", map[string]int{"minutes": 61}, 400},
		{"/dash/boxes/nobox/extend", map[string]int{"minutes": 5}, 404},
		{"/dash/boxes/nobox/end", map[string]int{}, 404},
	} {
		if w := e.do("POST", c.path, c.body, nil); w.Code != c.code {
			t.Errorf("%s %v: %d want %d (%s)", c.path, c.body, w.Code, c.code, w.Body)
		}
	}
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "seconds_left": 1690})
	if len(hb.Commands) != 1 || hb.Commands[0].Op != "extend" || hb.Commands[0].Minutes != 10 {
		t.Fatalf("commands %+v", hb.Commands)
	}
	cid := hb.Commands[0].ID
	// Picked commands are not sent again, and never to another box.
	if hb := heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active"}); len(hb.Commands) != 0 {
		t.Fatalf("command sent twice: %+v", hb.Commands)
	}
	// Result from the wrong box is refused.
	if w := e.do("POST", "/api/v1/commands/"+cid+"/result", map[string]any{"ok": true}, box(tok2)); w.Code != 404 {
		t.Errorf("foreign result: %d", w.Code)
	}
	if w := e.do("POST", "/api/v1/commands/999/result", map[string]any{"ok": true}, box(tok1)); w.Code != 404 {
		t.Errorf("unknown command: %d", w.Code)
	}
	for i := 0; i < 2; i++ { // reported twice, counted once
		w := e.do("POST", "/api/v1/commands/"+cid+"/result", map[string]any{"ok": true, "message": "extended by 10"}, box(tok1))
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
			t.Fatalf("result: %d %s", w.Code, w.Body)
		}
	}
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.ExtendedMinutes != 10 {
		t.Errorf("extended_minutes %d, want 10", ss.ExtendedMinutes)
	}

	// A failed extend does not count.
	e.do("POST", "/dash/boxes/hackbox1/extend", map[string]int{"minutes": 5}, nil)
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active"})
	e.do("POST", "/api/v1/commands/"+hb.Commands[0].ID+"/result", map[string]any{"ok": false, "message": "not active"}, box(tok1))
	if ss, _ := e.s.sessionBy("id", r.ID); ss.ExtendedMinutes != 10 {
		t.Errorf("failed extend counted: %d", ss.ExtendedMinutes)
	}

	// End goes to this box only.
	if w := e.do("POST", "/dash/boxes/hackbox1/end", map[string]any{}, nil); w.Code != 200 {
		t.Fatalf("end: %d", w.Code)
	}
	if hb := heartbeat(e, tok2, map[string]any{"state": "idle"}); len(hb.Commands) != 0 {
		t.Errorf("box2 got box1's command")
	}
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active"})
	if len(hb.Commands) != 1 || hb.Commands[0].Op != "end" {
		t.Errorf("end command %+v", hb.Commands)
	}

	// Offline after 15 s without a heartbeat.
	e.clk.Add(16 * time.Second)
	if b := findBox(dashState(e), "hackbox1"); b.Online {
		t.Errorf("box still online")
	}
}

func TestStaleCommandsExpire(t *testing.T) {
	e := newEnv(t, nil)
	e.do("POST", "/dash/boxes/hackbox1/end", map[string]any{}, nil)
	e.clk.Add(3 * time.Minute)
	if hb := heartbeat(e, tok1, map[string]any{"state": "idle"}); len(hb.Commands) != 0 {
		t.Fatalf("stale command delivered: %+v", hb.Commands)
	}
}

func TestHeartbeatForeignSessionIgnored(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	hb := heartbeat(e, tok2, map[string]any{"session_id": r.ID, "state": "active", "extend_request_at": 123})
	if hb.Extend.Status != "none" {
		t.Errorf("extend %+v", hb.Extend)
	}
	if b := findBox(dashState(e), "hackbox2"); b.SessionID != "" || b.Request != nil {
		t.Errorf("box2 took box1's session: %+v", b)
	}
	// A numeric session_id is accepted (and simply unknown here).
	heartbeat(e, tok1, map[string]any{"session_id": 42, "state": "idle"})
}

func TestExtendRequestFlow(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	asked := e.clk.Now().Unix()
	hb := heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "seconds_left": 120, "extend_request_at": asked})
	if hb.Extend.Status != "pending" {
		t.Fatalf("extend %+v", hb.Extend)
	}
	// Same asked_at again: still one request.
	heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "extend_request_at": asked})
	var n int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&n)
	if n != 1 {
		t.Fatalf("requests %d", n)
	}
	b := findBox(dashState(e), "hackbox1")
	if b.Request == nil || b.Request.Status != "pending" || b.Request.AskedAt != asked {
		t.Fatalf("dash request %+v", b.Request)
	}
	rid := b.Request.ID
	path := "/dash/requests/" + itoa(rid)
	if w := e.do("POST", path, map[string]any{"approve": true, "minutes": 10}, nil); w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", path, map[string]any{"approve": false}, nil); w.Code != 409 {
		t.Errorf("second decision: %d", w.Code)
	}
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active"})
	if hb.Extend.Status != "approved" || hb.Extend.Minutes != 10 {
		t.Errorf("extend %+v", hb.Extend)
	}
	if len(hb.Commands) != 1 || hb.Commands[0].Op != "extend" || hb.Commands[0].Minutes != 10 {
		t.Errorf("commands %+v", hb.Commands)
	}
	if b := findBox(dashState(e), "hackbox1"); b.Request != nil {
		t.Errorf("approved request still shown as pending")
	}

	// A second request, declined.
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "extend_request_at": asked + 60})
	if hb.Extend.Status != "pending" {
		t.Fatalf("second request %+v", hb.Extend)
	}
	rid2 := findBox(dashState(e), "hackbox1").Request.ID
	if w := e.do("POST", "/dash/requests/"+itoa(rid2), map[string]any{"approve": false}, nil); w.Code != 200 {
		t.Fatalf("decline: %d", w.Code)
	}
	hb = heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active"})
	if hb.Extend.Status != "declined" || len(hb.Commands) != 0 {
		t.Errorf("after decline %+v", hb)
	}
	if w := e.do("POST", "/dash/requests/9999", map[string]any{"approve": true}, nil); w.Code != 404 {
		t.Errorf("unknown request: %d", w.Code)
	}

	// A request from a session that is no longer on the box cannot be approved.
	heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "extend_request_at": asked + 120})
	rid3 := findBox(dashState(e), "hackbox1").Request.ID
	r2 := e.create(tok1, "Grace")
	heartbeat(e, tok1, map[string]any{"session_id": r2.ID, "state": "active"})
	if w := e.do("POST", "/dash/requests/"+itoa(rid3), map[string]any{"approve": true, "minutes": 5}, nil); w.Code != 409 {
		t.Errorf("approve for an old session: %d", w.Code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestDashGuard(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequest("POST", "/dash/boxes/hackbox1/end", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Errorf("text/plain: %d", w.Code)
	}
	if w := e.do("POST", "/dash/boxes/hackbox1/end", map[string]any{}, hdr{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("foreign origin: %d", w.Code)
	}
	if w := e.do("POST", "/dash/boxes/hackbox1/end", map[string]any{}, hdr{"Origin": "http://example.com"}); w.Code != 200 {
		t.Errorf("same origin: %d %s", w.Code, w.Body)
	}
}

func TestEndAndEmptyPage(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada Lovelace")
	if w := e.do("POST", "/api/v1/sessions/"+r.ID+"/end", map[string]any{"ended_at": 1, "reason": "x"}, box(tok2)); w.Code != 404 {
		t.Errorf("foreign end: %d", w.Code)
	}
	w := e.do("GET", "/d/"+r.Token, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Preparing") || !strings.Contains(w.Body.String(), `http-equiv="refresh" content="3"`) {
		t.Fatalf("preparing page: %d %s", w.Code, w.Body)
	}
	for _, want := range []string{"Hi Ada,", "hackbox1", "Thu 1 Oct 2026, 14:05", "0G hack-box"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("preparing page lacks %q", want)
		}
	}
	// Download before the archive exists goes back to the page.
	if w := e.do("GET", "/d/"+r.Token+"/download", nil, nil); w.Code != 303 {
		t.Errorf("early download: %d", w.Code)
	}
	ended := e.clk.Now().Unix() + 1800
	w = e.do("POST", "/api/v1/sessions/"+r.ID+"/end", map[string]any{"ended_at": ended, "reason": "timeout", "empty": true}, box(tok1))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("end: %d %s", w.Code, w.Body)
	}
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.EndedAt != ended || ss.EndReason != "timeout" || !ss.Empty || ss.ExpiresAt != ended+7*86400 {
		t.Errorf("after end %+v", ss)
	}
	w = e.do("GET", "/d/"+r.Token, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "There was nothing new in ~/project to save.") || strings.Contains(w.Body.String(), "refresh") {
		t.Errorf("empty page: %d %s", w.Code, w.Body)
	}
	hist := dashState(e).Sessions
	if len(hist) != 1 || hist[0].DurationSeconds != 1800 || hist[0].EndReason != "timeout" || !hist[0].Empty {
		t.Errorf("history %+v", hist)
	}
}

func TestArchiveUploadDownload(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	tgz := makeTarGz(t, goodTar)
	if w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", tgz, box(tok2)); w.Code != 404 {
		t.Errorf("foreign upload: %d", w.Code)
	}
	if w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", []byte("not a tarball"), box(tok1)); w.Code != 400 {
		t.Errorf("garbage upload: %d", w.Code)
	}
	trunc := tgz[:len(tgz)/2]
	if w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", trunc, box(tok1)); w.Code != 400 {
		t.Errorf("truncated upload: %d", w.Code)
	}
	w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", tgz, box(tok1))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	// Idempotent: a second upload replaces.
	if w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", tgz, box(tok1)); w.Code != 200 {
		t.Fatalf("re-upload: %d", w.Code)
	}
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.ArchiveBytes != int64(len(tgz)) || ss.ArchivePath != filepath.Join(e.s.archiveDir, r.ID+".tar.gz") {
		t.Errorf("stored %+v", ss)
	}
	left, _ := filepath.Glob(filepath.Join(e.s.archiveDir, ".upload-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}

	// An "empty" end after the archive does not hide the archive.
	e.do("POST", "/api/v1/sessions/"+r.ID+"/end", map[string]any{"ended_at": e.clk.Now().Unix(), "reason": "done", "empty": true}, box(tok1))

	w = e.do("GET", "/d/"+r.Token, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/d/`+r.Token+`/download"`) || strings.Contains(w.Body.String(), "refresh") {
		t.Fatalf("ready page: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/d/"+r.Token+"/download", nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %v", w.Code, w.Header())
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="hackbox-`+r.Code+`.zip"` {
		t.Errorf("disposition %q", cd)
	}
	files := zipNames(t, w.Body.Bytes())
	top := r.Code + "-project/"
	if files[top+"README.md"] != "# hello\n" || files[top+"src/main.py"] != "print('hi')\n" {
		t.Errorf("zip content %v", files)
	}
	if _, ok := files[top+"src/"]; !ok {
		t.Errorf("zip lacks dir entry: %v", files)
	}
	for n := range files {
		if !strings.HasPrefix(n, top) {
			t.Errorf("entry outside top folder: %s", n)
		}
	}

	// Staff download streams the same zip.
	w2 := e.do("GET", "/dash/sessions/"+r.ID+"/download", nil, nil)
	if w2.Code != 200 || !bytes.Equal(w2.Body.Bytes(), w.Body.Bytes()) {
		t.Errorf("staff download: %d", w2.Code)
	}
	if w := e.do("GET", "/dash/sessions/nope/download", nil, nil); w.Code != 404 {
		t.Errorf("staff download unknown: %d", w.Code)
	}
	hist := dashState(e).Sessions
	if hist[0].DownloadURL != "/dash/sessions/"+r.ID+"/download" || hist[0].ArchiveBytes != int64(len(tgz)) || hist[0].GitHubStatus != "disabled" {
		t.Errorf("history %+v", hist[0])
	}
}

func TestArchiveSizeCap(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.MaxArchive = 1024 })
	r := e.create(tok1, "Ada")
	big := makeTarGz(t, []tarEntry{{name: "project/rand.bin", typ: tar.TypeReg, body: randomString(8192)}})
	if len(big) <= 1024 {
		t.Fatalf("test archive too small: %d", len(big))
	}
	w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", big, box(tok1))
	if w.Code != 413 {
		t.Fatalf("over cap: %d %s", w.Code, w.Body)
	}
	if ss, _ := e.s.sessionBy("id", r.ID); ss.ArchivePath != "" {
		t.Errorf("oversized archive stored")
	}
	small := makeTarGz(t, goodTar)
	if w := e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", small, box(tok1)); w.Code != 200 {
		t.Errorf("under cap: %d", w.Code)
	}
}

func randomString(n int) string {
	var sb strings.Builder
	for sb.Len() < n {
		sb.WriteString(randomToken())
	}
	return sb.String()
}

func TestMaliciousTarball(t *testing.T) {
	tgz := makeTarGz(t, evilTar)
	var zbuf bytes.Buffer
	if err := tarGzToZip(bytes.NewReader(tgz), &zbuf, "ABC234-project/"); err != nil {
		t.Fatal(err)
	}
	files := zipNames(t, zbuf.Bytes())
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{"ABC234-project/", "ABC234-project/.git/config", "ABC234-project/ok.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("zip entries %v, want %v", names, want)
	}

	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	os.Mkdir(work, 0o700)
	if err := unpackTarGz(bytes.NewReader(tgz), work); err != nil {
		t.Fatal(err)
	}
	var got []string
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		rel, _ := filepath.Rel(dir, p)
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("symlink created: %s", rel)
		}
		if !fi.IsDir() {
			got = append(got, rel)
		}
		return nil
	})
	if strings.Join(got, ",") != filepath.Join("work", "ok.txt") {
		t.Errorf("unpacked %v", got)
	}
	if _, err := os.Stat("/etc/abs.txt"); err == nil {
		t.Errorf("absolute path written")
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"project/a.txt":  "a.txt",
		"./project/a/b":  "a/b",
		"project":        "",
		"project/":       "",
		"other/x":        "other/x",
		"/abs":           "",
		"a/../b":         "",
		"..":             "",
		"project/..":     "",
		"a\\b":           "",
		"":               "",
		"./":             "",
		"project//x.txt": "x.txt",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpiredAndUnknownPages(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	e.clk.Add(8 * 24 * time.Hour)
	w := e.do("GET", "/d/"+r.Token, nil, nil)
	if w.Code != 410 || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("expired page: %d", w.Code)
	}
	if w := e.do("GET", "/d/"+r.Token+"/download", nil, nil); w.Code != 410 {
		t.Errorf("expired download: %d", w.Code)
	}
	if w := e.do("GET", "/d/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, nil); w.Code != 404 {
		t.Errorf("unknown: %d", w.Code)
	}
	if w := e.do("GET", "/d/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/download", nil, nil); w.Code != 404 {
		t.Errorf("unknown download: %d", w.Code)
	}
	// Expired codes no longer resolve.
	w = e.do("POST", "/code", "code="+r.Code, hdr{"Content-Type": "application/x-www-form-urlencoded"})
	if w.Code != 404 {
		t.Errorf("expired code: %d", w.Code)
	}
}

func postCode(e *env, code, ip string) *httptest.ResponseRecorder {
	h := hdr{"Content-Type": "application/x-www-form-urlencoded"}
	if ip != "" {
		h["Cf-Connecting-IP"] = ip
	}
	return e.do("POST", "/code", "code="+url.QueryEscape(code), h)
}

func TestCodeLookupAndRateLimit(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	if w := e.do("GET", "/code", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `name="code"`) {
		t.Fatalf("code form: %d", w.Code)
	}
	w := postCode(e, strings.ToLower(r.Code[:3])+" "+strings.ToLower(r.Code[3:]), "198.51.100.1")
	if w.Code != 303 || w.Header().Get("Location") != "/d/"+r.Token {
		t.Fatalf("lookup: %d %v", w.Code, w.Header())
	}
	if w := postCode(e, "ZZZZZZ", "198.51.100.1"); w.Code != 404 || !strings.Contains(w.Body.String(), "No session") {
		t.Errorf("wrong code: %d", w.Code)
	}
	if w := postCode(e, "abc", "198.51.100.1"); w.Code != 400 {
		t.Errorf("short code: %d", w.Code)
	}
	postCode(e, "ZZZZZZ", "198.51.100.1")
	postCode(e, "ZZZZZZ", "198.51.100.1") // 5th try
	w = postCode(e, r.Code, "198.51.100.1")
	if w.Code != 429 || !strings.Contains(w.Body.String(), "Too many tries") {
		t.Fatalf("6th try: %d", w.Code)
	}
	// Another client is not affected; RemoteAddr counts when there is no Cf header.
	if w := postCode(e, r.Code, "198.51.100.2"); w.Code != 303 {
		t.Errorf("other ip: %d", w.Code)
	}
	if w := postCode(e, r.Code, ""); w.Code != 303 {
		t.Errorf("direct client: %d", w.Code)
	}
	// After a minute the first client may try again.
	e.clk.Add(61 * time.Second)
	if w := postCode(e, r.Code, "198.51.100.1"); w.Code != 303 {
		t.Errorf("after window: %d", w.Code)
	}
}

func TestAPITestPage(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.APITestPage = []byte("<html>api</html>") })
	if w := e.do("GET", "/dash/api_test.html", nil, nil); w.Code != 200 || w.Body.String() != "<html>api</html>" {
		t.Errorf("api test page: %d", w.Code)
	}
}

func TestSlugAndRepoName(t *testing.T) {
	cases := map[string]string{
		"Ada Lovelace":                         "ada-lovelace",
		"  ":                                   "anon",
		"José García!!":                        "jos-garc-a",
		"x---y":                                "x-y",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789": "abcdefghijklmnopqrstuvwxyz0123",
		"-lead-":                               "lead",
		"日本":                                   "anon",
	}
	for in, want := range cases {
		if got := slug(in, 30, "anon"); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	e := newEnv(t, nil)
	ss := &Session{Box: "hackbox1", Name: "Ada Lovelace", Code: "K7MZQ2", StartedAt: time.Date(2026, 10, 1, 9, 7, 0, 0, time.UTC).Unix()}
	if got := e.s.gh.repoName(ss); got != "20261001-0907-hackbox1-ada-lovelace-K7MZQ2" {
		t.Errorf("repo name %q", got)
	}
}

// ---------------------------------------------------------------------------
// GitHub worker

const ghToken = "ghs_SuperSecretToken123"

type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	bodies   []map[string]any
	paths    []string
	auths    []string
	exists   bool
	failWith int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.bodies = append(f.bodies, b)
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		switch {
		case f.failWith != 0:
			w.WriteHeader(f.failWith)
			io.WriteString(w, `{"message":"Bad credentials"}`)
		case f.exists:
			w.WriteHeader(422)
			io.WriteString(w, `{"message":"Repository creation failed.","errors":[{"resource":"Repository","code":"custom","field":"name","message":"name already exists on this account"}]}`)
		default:
			w.WriteHeader(201)
			io.WriteString(w, `{"id":1}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// fakeGit writes a shell script that records each call's arguments, and the
// files in the work tree on "add". With FAKE_GIT_FAIL set, push fails and
// echoes its arguments (token included) the way a careless tool might.
func fakeGit(t *testing.T) (cmd, argsLog, filesLog string) {
	dir := t.TempDir()
	cmd = filepath.Join(dir, "git")
	argsLog = filepath.Join(dir, "args.log")
	filesLog = filepath.Join(dir, "files.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + argsLog + `"
for a in "$@"; do
  case "$a" in
    add) find . -type f | LC_ALL=C sort >> "` + filesLog + `" ;;
    push)
      if [ -n "$FAKE_GIT_FAIL" ]; then
        echo "fatal: unable to access $*: The requested URL returned error: 403" >&2
        exit 128
      fi ;;
  esac
done
exit 0
`
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func ghEnv(t *testing.T, gh *fakeGitHub, gitCmd string) *env {
	return newEnv(t, func(o *Options) {
		o.Config.GitHub.Token = ghToken
		o.GitHubAPI = gh.srv.URL
		o.GitCmd = gitCmd
	})
}

func TestGitHubWorkerPush(t *testing.T) {
	gh := newFakeGitHub(t)
	git, argsLog, filesLog := fakeGit(t)
	e := ghEnv(t, gh, git)
	r := e.create(tok1, "Ada Lovelace")
	if ss, _ := e.s.sessionBy("id", r.ID); ss.GitHubStatus != "disabled" {
		t.Logf("status before archive: %s", ss.GitHubStatus)
	}
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, append(append([]tarEntry{}, goodTar...), evilTar...)), box(tok1))
	if ss, _ := e.s.sessionBy("id", r.ID); ss.GitHubStatus != "queued" {
		t.Fatalf("status after upload: %s", ss.GitHubStatus)
	}
	e.s.gh.drain(context.Background())

	ss, _ := e.s.sessionBy("id", r.ID)
	wantRepo := "20261001-1405-hackbox1-ada-lovelace-" + r.Code
	if ss.GitHubStatus != "pushed" || ss.GitHubRepo != wantRepo || ss.GitHubError != "" {
		t.Fatalf("after push: status %s repo %s err %s", ss.GitHubStatus, ss.GitHubRepo, ss.GitHubError)
	}
	if len(gh.paths) != 1 || gh.paths[0] != "POST /orgs/0g-hackbox-sessions/repos" || gh.auths[0] != "Bearer "+ghToken {
		t.Fatalf("api calls %v %v", gh.paths, gh.auths)
	}
	b := gh.bodies[0]
	if b["name"] != wantRepo || b["private"] != true || b["description"] != "hack-box session hackbox1 Ada Lovelace 2026-10-01 14:05" {
		t.Errorf("create body %v", b)
	}
	args, _ := os.ReadFile(argsLog)
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 4 {
		t.Fatalf("git calls %q", lines)
	}
	for i, want := range []string{" init -q -b main", " add -A", " commit -q --allow-empty -m Session " + r.Code + " on hackbox1, Ada Lovelace",
		" push -q --force https://x-access-token:" + ghToken + "@github.com/0g-hackbox-sessions/" + wantRepo + ".git main"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("git call %d %q lacks %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[2], "user.name=hack-box") || !strings.Contains(lines[2], "user.email=hackbox@0g.ai") {
		t.Errorf("commit identity: %q", lines[2])
	}
	files, _ := os.ReadFile(filesLog)
	if strings.TrimSpace(string(files)) != "./README.md\n./ok.txt\n./src/main.py" {
		t.Errorf("work tree files %q", files)
	}
	if strings.Contains(e.log.String(), ghToken) {
		t.Errorf("token in log:\n%s", e.log.String())
	}
	if !strings.Contains(e.log.String(), "pushed to 0g-hackbox-sessions/"+wantRepo) {
		t.Errorf("log lacks push line:\n%s", e.log.String())
	}
	hist := dashState(e).Sessions
	if hist[0].GitHubURL != "https://github.com/0g-hackbox-sessions/"+wantRepo {
		t.Errorf("github url %q", hist[0].GitHubURL)
	}
	if strings.Contains(fmtJSON(dashState(e)), ghToken) {
		t.Errorf("token in dash state")
	}

	// Nothing more to do.
	e.s.gh.drain(context.Background())
	if len(gh.paths) != 1 {
		t.Errorf("pushed twice")
	}
}

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestGitHubWorkerRepoExists(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.exists = true
	git, _, _ := fakeGit(t)
	e := ghEnv(t, gh, git)
	r := e.create(tok1, "")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	e.s.gh.drain(context.Background())
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.GitHubStatus != "pushed" || !strings.Contains(ss.GitHubRepo, "-hackbox1-anon-"+r.Code) {
		t.Errorf("status %s repo %s err %s", ss.GitHubStatus, ss.GitHubRepo, ss.GitHubError)
	}
}

func TestGitHubWorkerFailureRedactsAndRetries(t *testing.T) {
	gh := newFakeGitHub(t)
	git, argsLog, _ := fakeGit(t)
	t.Setenv("FAKE_GIT_FAIL", "1")
	e := ghEnv(t, gh, git)
	r := e.create(tok1, "Ada")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	e.s.gh.drain(context.Background())

	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.GitHubStatus != "failed" || !strings.Contains(ss.GitHubError, "git push") || !strings.Contains(ss.GitHubError, "***") {
		t.Fatalf("status %s err %q", ss.GitHubStatus, ss.GitHubError)
	}
	if strings.Contains(ss.GitHubError, ghToken) {
		t.Errorf("token in github_error")
	}
	if strings.Contains(e.log.String(), ghToken) {
		t.Errorf("token in log:\n%s", e.log.String())
	}
	if strings.Contains(fmtJSON(dashState(e)), ghToken) {
		t.Errorf("token in dash state")
	}
	if args, _ := os.ReadFile(argsLog); !strings.Contains(string(args), ghToken) {
		t.Errorf("fake git never saw the push url; test is not proving redaction")
	}

	// Not retried before 5 minutes.
	calls := len(gh.paths)
	e.s.gh.drain(context.Background())
	if len(gh.paths) != calls {
		t.Errorf("retried too early")
	}
	os.Unsetenv("FAKE_GIT_FAIL")
	e.clk.Add(5*time.Minute + time.Second)
	e.s.gh.drain(context.Background())
	ss, _ = e.s.sessionBy("id", r.ID)
	if ss.GitHubStatus != "pushed" || ss.GitHubError != "" {
		t.Errorf("retry: %s %q", ss.GitHubStatus, ss.GitHubError)
	}
}

func TestGitHubWorkerAPIFailure(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.failWith = 401
	git, argsLog, _ := fakeGit(t)
	e := ghEnv(t, gh, git)
	r := e.create(tok1, "Ada")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	e.s.gh.drain(context.Background())
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.GitHubStatus != "failed" || !strings.Contains(ss.GitHubError, "HTTP 401") {
		t.Errorf("status %s err %q", ss.GitHubStatus, ss.GitHubError)
	}
	if b, _ := os.ReadFile(argsLog); len(b) != 0 {
		t.Errorf("git ran after the API failed")
	}
	if strings.Contains(e.log.String(), ghToken) {
		t.Errorf("token in log")
	}
}

func TestGitHubWorkerRunLoop(t *testing.T) {
	gh := newFakeGitHub(t)
	git, _, _ := fakeGit(t)
	e := ghEnv(t, gh, git)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.RunWorker(ctx); close(done) }()
	r := e.create(tok1, "Ada")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	deadline := time.Now().Add(5 * time.Second)
	for {
		ss, _ := e.s.sessionBy("id", r.ID)
		if ss.GitHubStatus == "pushed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not push: %s %s", ss.GitHubStatus, ss.GitHubError)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestDisabledWithoutToken(t *testing.T) {
	e := newEnv(t, nil)
	r := e.create(tok1, "Ada")
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	ss, _ := e.s.sessionBy("id", r.ID)
	if ss.GitHubStatus != "disabled" {
		t.Errorf("status %s", ss.GitHubStatus)
	}
	// RunWorker returns at once when disabled.
	done := make(chan struct{})
	go func() { e.s.RunWorker(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled worker did not return")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"public_base_url":"https://h.example/","boxes":{"devtoken":"hackbox1"},"github":{"org":"o","token":""}}`), 0o600)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicBaseURL != "https://h.example" || c.DownloadDays != 7 || c.Boxes["devtoken"] != "hackbox1" {
		t.Errorf("config %+v", c)
	}
	os.WriteFile(p, []byte(`{"boxes":{}}`), 0o600)
	if _, err := LoadConfig(p); err == nil {
		t.Errorf("missing public_base_url accepted")
	}
}
