package hub

import (
	"strings"
	"testing"
)

func TestCleanEmail(t *testing.T) {
	cases := map[string]string{
		"ada@example.com":           "ada@example.com",
		"  ada.l+hack@mail.co.uk  ": "ada.l+hack@mail.co.uk",
		"":                          "",
		"ada":                       "",
		"ada@example":               "",
		"@example.com":              "",
		"ada@.com":                  "",
		"ada@exa..mple.com":         "",
		"a b@example.com":           "",
		"ada@@example.com":          "",
		"<ada@example.com>":         "",
		"ada@example.c":             "",
		"ada\x07@example.com":       "",
		strings.Repeat("a", 110) + "@example.com": "",
	}
	for in, want := range cases {
		if got := cleanEmail(in); got != want {
			t.Errorf("cleanEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionEmail(t *testing.T) {
	e := newEnv(t, nil)
	start := func(tok string, email any) createSessionResp {
		w := e.do("POST", "/api/v1/sessions", map[string]any{"name": "Ada Lovelace", "agent": "claude", "minutes": 30, "email": email}, box(tok))
		if w.Code != 200 {
			t.Fatalf("start with email %v: %d %s", email, w.Code, w.Body)
		}
		return decode[createSessionResp](t, w)
	}
	bad := start(tok2, "not an email")
	if ss, _ := e.s.sessionBy("id", bad.ID); ss.Email != "" {
		t.Errorf("invalid email stored: %q", ss.Email)
	}
	r := start(tok1, " ada@example.com ")
	heartbeat(e, tok1, map[string]any{"session_id": r.ID, "state": "active", "seconds_left": 100})
	st := dashState(e)
	if b := findBox(st, "hackbox1"); b.Email != "ada@example.com" || b.Attendee != "Ada Lovelace" {
		t.Errorf("box card email %+v", b)
	}
	if st.Sessions[0].ID != r.ID || st.Sessions[0].Email != "ada@example.com" {
		t.Errorf("history email %+v", st.Sessions[0])
	}
	// Never on the public page, in any state.
	for _, p := range []string{"/d/" + r.Token} {
		if w := e.do("GET", p, nil, nil); strings.Contains(w.Body.String(), "ada@example.com") || strings.Contains(w.Body.String(), "example.com") {
			t.Errorf("email on public page %s", p)
		}
	}
	e.do("PUT", "/api/v1/sessions/"+r.ID+"/archive", makeTarGz(t, goodTar), box(tok1))
	if w := e.do("GET", "/d/"+r.Token, nil, nil); w.Code != 200 || strings.Contains(w.Body.String(), "example.com") {
		t.Errorf("email on ready page")
	}
	// A non-string email does not fail the start either.
	w := e.do("POST", "/api/v1/sessions", map[string]any{"name": "x", "email": 42}, box(tok1))
	if w.Code != 200 {
		t.Errorf("numeric email: %d", w.Code)
	}
}

func TestFinish(t *testing.T) {
	e := newEnv(t, nil)
	fin := func(h hdr) int { return e.do("POST", "/dash/boxes/hackbox1/finish", map[string]any{}, h).Code }
	if c := fin(nil); c != 409 {
		t.Errorf("never seen: %d", c)
	}
	heartbeat(e, tok1, map[string]any{"state": "active", "seconds_left": 30})
	if c := fin(nil); c != 409 {
		t.Errorf("active: %d", c)
	}
	if c := e.do("POST", "/dash/boxes/nobox/finish", map[string]any{}, nil).Code; c != 404 {
		t.Errorf("unknown box: %d", c)
	}
	heartbeat(e, tok1, map[string]any{"state": "ending"})
	if c := fin(hdr{"Origin": "https://evil.example"}); c != 403 {
		t.Errorf("cross-origin: %d", c)
	}
	if c := fin(nil); c != 200 {
		t.Fatalf("ending: %d", c)
	}
	w := e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "ending"}, box(tok1))
	if !strings.Contains(w.Body.String(), `"op":"done"`) {
		t.Fatalf("heartbeat reply %s", w.Body)
	}
	hb := decode[heartbeatResp](t, w)
	if len(hb.Commands) != 1 || hb.Commands[0].Op != "done" || hb.Commands[0].Minutes != 0 || hb.Commands[0].ID == "" {
		t.Errorf("commands %+v", hb.Commands)
	}
	if w := e.do("POST", "/api/v1/commands/"+hb.Commands[0].ID+"/result", map[string]any{"ok": true}, box(tok1)); w.Code != 200 {
		t.Errorf("result: %d", w.Code)
	}
}
