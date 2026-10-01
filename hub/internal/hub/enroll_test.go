package hub

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

const enrollTok = "fleet-enroll-token-0123456789"

func enrollEnv(t *testing.T) *env {
	return newEnv(t, func(o *Options) { o.Config.EnrollToken = enrollTok })
}

func enroll(e *env, mac, ip string) enrollResp {
	e.t.Helper()
	w := e.do("POST", "/api/v1/enroll", map[string]string{"mac": mac, "current_hostname": "hackbox-" + mac[12:14] + mac[15:]},
		hdr{"Authorization": "Bearer " + enrollTok, "X-Test-Remote": ip + ":4000"})
	if w.Code != 200 {
		e.t.Fatalf("enroll %s: %d %s", mac, w.Code, w.Body)
	}
	return decode[enrollResp](e.t, w)
}

func mac(n int) string { return fmt.Sprintf("aa:bb:cc:00:00:%02x", n) }

func TestEnrollSameMACSameName(t *testing.T) {
	e := enrollEnv(t)
	// Config has hackbox1 and hackbox2 as unused tokens: they are free.
	r1 := enroll(e, mac(1), "100.64.0.1")
	if r1.Name != "hackbox1" || !regexp.MustCompile(`^[a-z2-7]{40}$`).MatchString(r1.Token) {
		t.Fatalf("first enroll %+v", r1)
	}
	// The enrolled token works as a box token.
	if hb := e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, box(r1.Token)); hb.Code != 200 {
		t.Fatalf("enrolled token heartbeat: %d", hb.Code)
	}
	if b := findBox(dashState(e), "hackbox1"); !b.Enrolled || b.MAC != mac(1) || !b.Online {
		t.Errorf("dash box %+v", b)
	}
	// Reinstall: same name, new token, old token revoked.
	r1b := enroll(e, strings.ToUpper(mac(1)), "100.64.0.1")
	if r1b.Name != "hackbox1" || r1b.Token == r1.Token {
		t.Fatalf("re-enroll %+v", r1b)
	}
	if w := e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, box(r1.Token)); w.Code != 401 {
		t.Errorf("old token still works: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/config", nil, box(r1b.Token)); w.Code != 200 {
		t.Errorf("new token: %d", w.Code)
	}
	if !strings.Contains(e.log.String(), "enroll: "+mac(1)+" -> hackbox1") {
		t.Errorf("enroll not logged:\n%s", e.log.String())
	}
	for _, tok := range []string{r1.Token, r1b.Token, enrollTok} {
		if strings.Contains(e.log.String(), tok) {
			t.Errorf("token in log")
		}
	}
}

func TestEnrollLowestFreeName(t *testing.T) {
	e := enrollEnv(t)
	// hackbox2 has sent a heartbeat with its config token: taken.
	heartbeat(e, tok2, map[string]any{"state": "idle"})
	// hackbox4 too, through a token that is no longer in the config.
	e.s.db.Exec(`INSERT INTO boxes (name, last_seen) VALUES ('hackbox4', 1)`)
	got := []string{}
	for i := 1; i <= 4; i++ {
		got = append(got, enroll(e, mac(i), "100.64.0.9").Name)
	}
	if strings.Join(got, ",") != "hackbox1,hackbox3,hackbox5,hackbox6" {
		t.Fatalf("names %v", got)
	}
	// The config token for hackbox1 (bound now) still maps to the same box.
	if w := e.do("GET", "/api/v1/config", nil, box(tok1)); w.Code != 200 {
		t.Errorf("config token after binding: %d", w.Code)
	}
	st := dashState(e)
	var names []string
	for _, b := range st.Boxes {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "hackbox1,hackbox2,hackbox3,hackbox4,hackbox5,hackbox6" {
		t.Errorf("dash boxes %v", names)
	}
	// Enrolled boxes get keys panel rows too.
	cfg := decode[dashConfigResp](t, e.do("GET", "/dash/config", nil, nil))
	if len(cfg.Scopes) != 6 { // "*", hackbox1, hackbox2, hackbox3, hackbox5, hackbox6
		t.Errorf("config scopes %d", len(cfg.Scopes))
	}
	if w := setConfig(e, map[string]any{"scope": "hackbox5", "set": map[string]string{"agents": "claude"}}); w.Code != 200 {
		t.Errorf("config for an enrolled box: %d %s", w.Code, w.Body)
	}
}

func TestEnrollPrefix(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.EnrollToken = enrollTok; o.Config.NamePrefix = "box-" })
	if r := enroll(e, mac(7), "100.64.0.1"); r.Name != "box-1" {
		t.Errorf("prefix name %q", r.Name)
	}
}

func TestEnrollAuthAndValidation(t *testing.T) {
	e := enrollEnv(t)
	body := map[string]string{"mac": mac(1), "current_hostname": "x"}
	for _, h := range []hdr{nil, box(tok1), box("wrong-enroll-token-xxxxx"), {"Authorization": enrollTok}} {
		if w := e.do("POST", "/api/v1/enroll", body, h); w.Code != 401 {
			t.Errorf("enroll with %v: %d", h, w.Code)
		}
	}
	// The enroll token is not a box token anywhere else.
	for _, c := range []struct{ m, p string }{{"POST", "/api/v1/heartbeat"}, {"GET", "/api/v1/config"}, {"POST", "/api/v1/sessions"}} {
		if w := e.do(c.m, c.p, "{}", box(enrollTok)); w.Code != 401 {
			t.Errorf("%s %s with the enroll token: %d", c.m, c.p, w.Code)
		}
	}
	for _, m := range []string{"", "aa:bb:cc:dd:ee", "aa-bb-cc-dd-ee-ff", "aa:bb:cc:dd:ee:fg", "aabbccddeeff", "00:00:00:00:00:00", "aa:bb:cc:dd:ee:ff:00"} {
		w := e.do("POST", "/api/v1/enroll", map[string]string{"mac": m}, hdr{"Authorization": "Bearer " + enrollTok, "X-Test-Remote": "100.64.1.1:1"})
		if w.Code != 400 {
			t.Errorf("mac %q: %d", m, w.Code)
		}
		e.clk.Add(7 * time.Second) // stay under the rate limit
	}
	// Enrollment off without an enroll token.
	off := newEnv(t, nil)
	if w := off.do("POST", "/api/v1/enroll", body, hdr{"Authorization": "Bearer "}); w.Code != 401 {
		t.Errorf("enroll disabled: %d", w.Code)
	}
	// Through the tunnel: 404.
	if w := e.do("POST", "/api/v1/enroll", body, hdr{"Authorization": "Bearer " + enrollTok, "Cf-Connecting-IP": "203.0.113.1"}); w.Code != 404 {
		t.Errorf("enroll via tunnel: %d", w.Code)
	}
}

func TestEnrollConfigValidation(t *testing.T) {
	c := Config{PublicBaseURL: "https://x", EnrollToken: "short"}
	if err := c.normalize(); err == nil {
		t.Errorf("short enroll token accepted")
	}
	c = Config{PublicBaseURL: "https://x", EnrollToken: "same-token-as-a-box-123", Boxes: map[string]string{"same-token-as-a-box-123": "hackbox1"}}
	if err := c.normalize(); err == nil {
		t.Errorf("enroll token equal to a box token accepted")
	}
	c = Config{PublicBaseURL: "https://x"}
	if err := c.normalize(); err != nil || c.NamePrefix != "hackbox" {
		t.Errorf("default prefix %q %v", c.NamePrefix, err)
	}
}

func TestEnrollRateLimit(t *testing.T) {
	e := enrollEnv(t)
	for i := 1; i <= 10; i++ {
		enroll(e, mac(i), "100.64.2.2")
	}
	w := e.do("POST", "/api/v1/enroll", map[string]string{"mac": mac(11)}, hdr{"Authorization": "Bearer " + enrollTok, "X-Test-Remote": "100.64.2.2:1"})
	if w.Code != 429 {
		t.Fatalf("11th enroll: %d", w.Code)
	}
	// Wrong tokens count too.
	for i := 0; i < 10; i++ {
		e.do("POST", "/api/v1/enroll", "{}", hdr{"Authorization": "Bearer nope", "X-Test-Remote": "100.64.3.3:1"})
	}
	if w := e.do("POST", "/api/v1/enroll", "{}", hdr{"Authorization": "Bearer " + enrollTok, "X-Test-Remote": "100.64.3.3:1"}); w.Code != 429 {
		t.Errorf("guessing not limited: %d", w.Code)
	}
	enroll(e, mac(12), "100.64.4.4") // another address is fine
	e.clk.Add(61 * time.Second)
	enroll(e, mac(11), "100.64.2.2")
}

func TestRelease(t *testing.T) {
	e := enrollEnv(t)
	r := enroll(e, mac(1), "100.64.0.1")
	heartbeat(e, r.Token, map[string]any{"state": "idle"})
	rel := func(name string) int {
		return e.do("POST", "/dash/boxes/"+name+"/release", map[string]any{}, nil).Code
	}
	if c := rel("hackbox1"); c != 409 {
		t.Fatalf("release while online: %d", c)
	}
	e.clk.Add(4 * time.Minute)
	if c := rel("hackbox1"); c != 409 {
		t.Fatalf("release 4 min after a heartbeat: %d", c)
	}
	e.clk.Add(61 * time.Second)
	if c := rel("hackbox1"); c != 200 {
		t.Fatalf("release: %d", c)
	}
	if w := e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, box(r.Token)); w.Code != 401 {
		t.Errorf("released token still works: %d", w.Code)
	}
	// A new MAC gets the freed name.
	if n := enroll(e, mac(2), "100.64.0.2").Name; n != "hackbox1" {
		t.Errorf("freed name not reused: %s", n)
	}
	if c := rel("nobox"); c != 404 {
		t.Errorf("release unknown: %d", c)
	}
	// Same guards as the other dashboard POSTs.
	if w := e.do("POST", "/dash/boxes/hackbox1/release", map[string]any{}, hdr{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("cross-origin release: %d", w.Code)
	}
	// A configured box that went quiet can be released too (its heartbeat record goes).
	heartbeat(e, tok2, map[string]any{"state": "idle"})
	e.clk.Add(6 * time.Minute)
	if c := rel("hackbox2"); c != 200 {
		t.Errorf("release config box: %d", c)
	}
	if n := enroll(e, mac(3), "100.64.0.3").Name; n != "hackbox2" {
		t.Errorf("released config name not reused: %s", n)
	}
}

func TestActivity(t *testing.T) {
	e := newEnv(t, nil)
	long := strings.Repeat("x", 33)
	many := []any{}
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("App%d", i))
	}
	cases := []struct {
		state string
		act   any
		want  string
	}{
		{"active", []any{"Terminal", "OpenCode", "Chromium"}, `["Terminal","OpenCode","Chromium"]`},
		{"active", []any{"Terminal", "", "  ", long, 42, nil, "bad\x07bell", "Files", " Editor "}, `["Terminal","Files","Editor"]`},
		{"active", many, `["App0","App1","App2","App3","App4","App5","App6","App7","App8","App9","App10","App11"]`},
		{"active", "Terminal", `[]`},
		{"active", nil, `[]`},
		{"ending", []any{"Terminal"}, `["Terminal"]`},
		{"idle", []any{"Terminal"}, `[]`},
	}
	for _, c := range cases {
		body := map[string]any{"state": c.state}
		if c.act != nil {
			body["activity"] = c.act
		}
		heartbeat(e, tok1, body)
		got, _ := json.Marshal(findBox(dashState(e), "hackbox1").Activity)
		if string(got) != c.want {
			t.Errorf("%s %v: got %s, want %s", c.state, c.act, got, c.want)
		}
	}
	// A box never seen has an empty list, not null.
	if a := findBox(dashState(e), "hackbox2").Activity; a == nil || len(a) != 0 {
		t.Errorf("unseen box activity %v", a)
	}
}

func TestDashboardRefusedFromBoxAddress(t *testing.T) {
	e := enrollEnv(t)
	boxIP := hdr{"X-Test-Remote": "100.64.9.9:4000"}
	staff := hdr{"X-Test-Remote": "100.64.1.1:4000"}
	paths := []string{"/", "/dash/state", "/dash/config", "/dash/api_test.html", "/dash/sessions/x/download"}
	// Before any box call the address is fine.
	if w := e.do("GET", "/dash/state", nil, boxIP); w.Code != 200 {
		t.Fatalf("before: %d", w.Code)
	}
	// A failed box call does not count.
	e.do("POST", "/api/v1/heartbeat", "{}", hdr{"Authorization": "Bearer wrong-token-xx", "X-Test-Remote": "100.64.9.9:4000"})
	if w := e.do("GET", "/dash/state", nil, boxIP); w.Code != 200 {
		t.Fatalf("after a failed call: %d", w.Code)
	}
	e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, hdr{"Authorization": "Bearer " + tok1, "X-Test-Remote": "100.64.9.9:4000"})
	for _, p := range paths {
		w := e.do("GET", p, nil, boxIP)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "not available from a hack box") {
			t.Errorf("GET %s from a box address: %d", p, w.Code)
		}
		if w := e.do("GET", p, nil, staff); w.Code == 403 {
			t.Errorf("GET %s from staff: 403", p)
		}
	}
	if w := e.do("POST", "/dash/boxes/hackbox1/end", map[string]any{}, boxIP); w.Code != 403 {
		t.Errorf("POST from a box address: %d", w.Code)
	}
	// The box API and the public pages still work from the box address.
	if w := e.do("GET", "/api/v1/config", nil, hdr{"Authorization": "Bearer " + tok1, "X-Test-Remote": "100.64.9.9:4000"}); w.Code != 200 {
		t.Errorf("box API from box: %d", w.Code)
	}
	if w := e.do("GET", "/code", nil, boxIP); w.Code != 200 {
		t.Errorf("/code from box: %d", w.Code)
	}
	// The enroll token marks the address as well.
	enroll(e, mac(1), "100.64.8.8")
	if w := e.do("GET", "/", nil, hdr{"X-Test-Remote": "100.64.8.8:1"}); w.Code != 403 {
		t.Errorf("after enroll: %d", w.Code)
	}
	// Loopback is never locked out.
	e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, hdr{"Authorization": "Bearer " + tok1, "X-Test-Remote": "127.0.0.1:4000"})
	if w := e.do("GET", "/", nil, hdr{"X-Test-Remote": "127.0.0.1:4001"}); w.Code != 200 {
		t.Errorf("loopback dashboard: %d", w.Code)
	}
	// After 24 h without box calls the address may use the dashboard again.
	e.clk.Add(24*time.Hour + time.Minute)
	if w := e.do("GET", "/dash/state", nil, boxIP); w.Code != 200 {
		t.Errorf("after 24 h: %d", w.Code)
	}
}
