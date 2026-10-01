package hub

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	keyA   = "sk-ant-default-AAAAAAAA"
	keyB   = "sk-ant-box1-BBBBBBBBBB"
	router = "0g-router-CCCCCCCCCC"
)

func setConfig(e *env, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do("POST", "/dash/config", body, nil)
}

func mustSetConfig(e *env, body map[string]any) dashConfigResp {
	e.t.Helper()
	w := setConfig(e, body)
	if w.Code != 200 {
		e.t.Fatalf("POST /dash/config %v: %d %s", body, w.Code, w.Body)
	}
	return decode[dashConfigResp](e.t, w)
}

func boxConfig(e *env, tok string) boxConfigResp {
	e.t.Helper()
	w := e.do("GET", "/api/v1/config", nil, box(tok))
	if w.Code != 200 {
		e.t.Fatalf("GET /api/v1/config: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"secrets":{`) {
		e.t.Errorf("secrets must always be an object: %s", w.Body)
	}
	return decode[boxConfigResp](e.t, w)
}

func TestConfigVersion(t *testing.T) {
	if v := configVersion(map[string]string{}); v != "" {
		t.Errorf("empty config version %q", v)
	}
	a := configVersion(map[string]string{"agents": "claude", cfgAnthropic: keyA})
	b := configVersion(map[string]string{cfgAnthropic: keyA, "agents": "claude"})
	if a != b || len(a) != 16 {
		t.Errorf("version not stable or wrong length: %q %q", a, b)
	}
	if c := configVersion(map[string]string{"agents": "claude", cfgAnthropic: keyB}); c == a {
		t.Errorf("version did not change with the value")
	}
}

func TestEffectiveConfigResolution(t *testing.T) {
	e := newEnv(t, nil)
	// Nothing set: empty version, empty secrets, no agents.
	c := boxConfig(e, tok1)
	if c.Version != "" || len(c.Secrets) != 0 || c.Agents != nil {
		t.Fatalf("empty config %+v", c)
	}
	if w := e.do("GET", "/api/v1/config", nil, nil); !strings.Contains(w.Body.String(), "unknown box token") {
		t.Fatalf("no auth: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/config", nil, box("wrong-token-xyz")); w.Code != 401 {
		t.Errorf("wrong token: %d", w.Code)
	}

	mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: keyA, cfgRouter: router, "agents": "claude  opencode"}})
	c1 := boxConfig(e, tok1)
	c2 := boxConfig(e, tok2)
	if c1.Secrets[cfgAnthropic] != keyA || c1.Secrets[cfgRouter] != router || strings.Join(c1.Agents, " ") != "claude opencode" {
		t.Fatalf("default config %+v", c1)
	}
	if c1.Version == "" || c1.Version != c2.Version {
		t.Errorf("both boxes should share the default version: %q %q", c1.Version, c2.Version)
	}
	// Stable when nothing changes, also after re-saving the same value.
	e.clk.Add(time.Minute)
	mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: keyA}})
	if v := boxConfig(e, tok1).Version; v != c1.Version {
		t.Errorf("version changed without a value change")
	}

	// Box override.
	mustSetConfig(e, map[string]any{"scope": "hackbox1", "set": map[string]string{cfgAnthropic: keyB, "agents": "claude-0g"}})
	o1 := boxConfig(e, tok1)
	if o1.Secrets[cfgAnthropic] != keyB || o1.Secrets[cfgRouter] != router || strings.Join(o1.Agents, " ") != "claude-0g" {
		t.Fatalf("override %+v", o1)
	}
	if o1.Version == c1.Version {
		t.Errorf("override did not change the version")
	}
	if v := boxConfig(e, tok2).Version; v != c1.Version {
		t.Errorf("box2 affected by box1 override")
	}

	// Clear at box scope falls back to the default.
	mustSetConfig(e, map[string]any{"scope": "hackbox1", "clear": []string{cfgAnthropic, "agents"}})
	f1 := boxConfig(e, tok1)
	if f1.Secrets[cfgAnthropic] != keyA || strings.Join(f1.Agents, " ") != "claude opencode" || f1.Version != c1.Version {
		t.Errorf("fallback %+v", f1)
	}

	// Clear at default scope removes the name for boxes without an override.
	mustSetConfig(e, map[string]any{"scope": "hackbox2", "set": map[string]string{cfgRouter: "box2-router-key"}})
	mustSetConfig(e, map[string]any{"scope": "*", "clear": []string{cfgRouter, "agents"}})
	if c := boxConfig(e, tok1); c.Secrets[cfgRouter] != "" || c.Agents != nil || len(c.Secrets) != 1 {
		t.Errorf("after default clear box1 %+v", c)
	}
	if c := boxConfig(e, tok2); c.Secrets[cfgRouter] != "box2-router-key" {
		t.Errorf("box2 override lost %+v", c)
	}
}

func TestDashConfigNeverReturnsValues(t *testing.T) {
	e := newEnv(t, nil)
	resp := mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: keyA, "agents": "claude opencode"}})
	mustSetConfig(e, map[string]any{"scope": "hackbox2", "set": map[string]string{cfgRouter: router}})
	w := e.do("GET", "/dash/config", nil, nil)
	if w.Code != 200 {
		t.Fatalf("GET /dash/config %d", w.Code)
	}
	for _, body := range []string{w.Body.String(), fmtJSON(resp), e.do("GET", "/dash/state", nil, nil).Body.String()} {
		for _, v := range []string{keyA, router} {
			if strings.Contains(body, v) {
				t.Fatalf("secret value leaked in %s", body)
			}
		}
	}
	if strings.Contains(e.log.String(), keyA) || strings.Contains(e.log.String(), router) {
		t.Errorf("secret value in log:\n%s", e.log.String())
	}
	st := decode[dashConfigResp](t, w)
	if len(st.Scopes) != 3 || st.Scopes[0].Scope != "*" || st.Scopes[1].Scope != "hackbox1" || st.Scopes[2].Scope != "hackbox2" {
		t.Fatalf("scopes %+v", st.Scopes)
	}
	all, b1, b2 := st.Scopes[0], st.Scopes[1], st.Scopes[2]
	if n := all.Names[cfgAnthropic]; !n.Set || n.Inherited || n.UpdatedAt == 0 {
		t.Errorf("default anthropic %+v", n)
	}
	if n := all.Names[cfgRouter]; n.Set || n.Inherited {
		t.Errorf("default router %+v", n)
	}
	if strings.Join(all.Agents, " ") != "claude opencode" {
		t.Errorf("default agents %v", all.Agents)
	}
	if n := b1.Names[cfgAnthropic]; n.Set || !n.Inherited {
		t.Errorf("box1 anthropic %+v", n)
	}
	if len(b1.Agents) != 0 || strings.Join(b1.EffectiveAgents, " ") != "claude opencode" || !b1.Names["agents"].Inherited {
		t.Errorf("box1 agents %+v", b1)
	}
	if n := b2.Names[cfgRouter]; !n.Set || n.Inherited {
		t.Errorf("box2 router %+v", n)
	}
	if b1.ConfigVersion == "" || b1.ConfigVersion == b2.ConfigVersion {
		t.Errorf("versions %q %q", b1.ConfigVersion, b2.ConfigVersion)
	}
	if !strings.Contains(e.log.String(), "config: scope *: set agents, set anthropic-api-key") {
		t.Errorf("config change not logged by name:\n%s", e.log.String())
	}
}

func TestConfigValidation(t *testing.T) {
	e := newEnv(t, nil)
	bad := []map[string]any{
		{"scope": "hackbox9", "set": map[string]string{cfgAnthropic: keyA}},
		{"scope": "", "set": map[string]string{cfgAnthropic: keyA}},
		{"scope": "*", "set": map[string]string{"github-token": keyA}},
		{"scope": "*", "clear": []string{"nope"}},
		{"scope": "*", "set": map[string]string{cfgAnthropic: "short"}},
		{"scope": "*", "set": map[string]string{cfgAnthropic: strings.Repeat("x", 513)}},
		{"scope": "*", "set": map[string]string{cfgAnthropic: "has a space in it"}},
		{"scope": "*", "set": map[string]string{cfgRouter: "tab\tinside-key"}},
		{"scope": "*", "set": map[string]string{cfgRouter: "non-ascii-ключ-key"}},
		{"scope": "*", "set": map[string]string{"agents": "claude vim"}},
		{"scope": "*", "set": map[string]string{"agents": "claude claude"}},
		{"scope": "*", "set": map[string]string{"agents": "   "}},
		{"scope": "*", "set": map[string]string{cfgAnthropic: keyA}, "clear": []string{cfgAnthropic}},
		{"scope": "*"},
	}
	for _, b := range bad {
		w := setConfig(e, b)
		if w.Code != 400 {
			t.Errorf("%v: got %d, want 400 (%s)", b, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "has a space") || strings.Contains(w.Body.String(), "inside-key") {
			t.Errorf("error echoes the value: %s", w.Body)
		}
	}
	// "agents": "   " is whitespace only, so it is not empty and fails; a truly empty value is ignored.
	if w := setConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: ""}}); w.Code != 400 {
		t.Errorf("only empty values: %d", w.Code)
	}
	if v := boxConfig(e, tok1).Version; v != "" {
		t.Errorf("failed posts changed the config: %q", v)
	}
	// Edge lengths are fine.
	mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: strings.Repeat("k", 8), cfgRouter: strings.Repeat("r", 512)}})
}

func TestConfigCSRFGuard(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"scope":"*","set":{"anthropic-api-key":"` + keyA + `"}}`
	req := httptest.NewRequest("POST", "/dash/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Errorf("text/plain: %d", w.Code)
	}
	if w := e.do("POST", "/dash/config", body, hdr{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("foreign origin: %d", w.Code)
	}
	if w := e.do("POST", "/dash/config", body, hdr{"Cf-Connecting-IP": "203.0.113.9"}); w.Code != 404 {
		t.Errorf("via tunnel: %d", w.Code)
	}
	if w := e.do("GET", "/dash/config", nil, hdr{"Cf-Connecting-IP": "203.0.113.9"}); w.Code != 404 {
		t.Errorf("GET via tunnel: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/config", nil, hdr{"Cf-Connecting-IP": "203.0.113.9", "Authorization": "Bearer " + tok1}); w.Code != 404 {
		t.Errorf("box config via tunnel: %d", w.Code)
	}
	if v := boxConfig(e, tok1).Version; v != "" {
		t.Errorf("guarded posts changed the config")
	}
}

func TestHeartbeatConfigVersion(t *testing.T) {
	e := newEnv(t, nil)
	hb := heartbeat(e, tok1, map[string]any{"state": "idle"})
	if hb.ConfigVersion != "" {
		t.Errorf("empty config version %q", hb.ConfigVersion)
	}
	w := e.do("POST", "/api/v1/heartbeat", map[string]any{"state": "idle"}, box(tok1))
	if !strings.Contains(w.Body.String(), `"config_version":""`) {
		t.Errorf("config_version must always be present: %s", w.Body)
	}
	b := findBox(dashState(e), "hackbox1")
	if !b.ConfigUpToDate || b.ConfigVersion != "" || b.AppliedConfigVersion != "" {
		t.Errorf("nothing set should read as up to date: %+v", b)
	}

	mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: keyA}})
	want := boxConfig(e, tok1).Version
	hb = heartbeat(e, tok1, map[string]any{"state": "active"})
	if hb.ConfigVersion != want {
		t.Fatalf("heartbeat config_version %q, want %q", hb.ConfigVersion, want)
	}
	b = findBox(dashState(e), "hackbox1")
	if b.ConfigUpToDate || b.ConfigVersion != want {
		t.Errorf("should be pending: %+v", b)
	}
	// The box applies it at idle and reports it.
	heartbeat(e, tok1, map[string]any{"state": "idle", "applied_config_version": want})
	b = findBox(dashState(e), "hackbox1")
	if !b.ConfigUpToDate || b.AppliedConfigVersion != want {
		t.Errorf("should be up to date: %+v", b)
	}
	// Omitting the field keeps the stored value.
	heartbeat(e, tok1, map[string]any{"state": "idle"})
	if b := findBox(dashState(e), "hackbox1"); b.AppliedConfigVersion != want {
		t.Errorf("applied version lost: %+v", b)
	}
	// A box override makes only that box pending.
	heartbeat(e, tok2, map[string]any{"state": "idle", "applied_config_version": want})
	mustSetConfig(e, map[string]any{"scope": "hackbox1", "set": map[string]string{cfgAnthropic: keyB}})
	st := dashState(e)
	if findBox(st, "hackbox1").ConfigUpToDate || !findBox(st, "hackbox2").ConfigUpToDate {
		t.Errorf("override pending state wrong: %+v", st.Boxes)
	}
}

func TestDatabaseFileMode(t *testing.T) {
	e := newEnv(t, nil)
	mustSetConfig(e, map[string]any{"scope": "*", "set": map[string]string{cfgAnthropic: keyA}})
	for _, name := range []string{"hub.db", "hub.db-wal", "hub.db-shm"} {
		fi, err := os.Stat(filepath.Join(e.s.dataDir, name))
		if err != nil {
			if name == "hub.db" {
				t.Fatal(err)
			}
			continue
		}
		if m := fi.Mode().Perm(); m&0o077 != 0 {
			t.Errorf("%s mode %o, want no group/other access", name, m)
		}
	}
}

func TestMigrationAddsAppliedColumn(t *testing.T) {
	dir := t.TempDir()
	// A database from the first hub release, before applied_config_version.
	old, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE boxes (name TEXT PRIMARY KEY, last_seen INTEGER NOT NULL DEFAULT 0,
		state TEXT NOT NULL DEFAULT '', seconds_left INTEGER NOT NULL DEFAULT 0,
		status_json TEXT NOT NULL DEFAULT '{}', session_id TEXT NOT NULL DEFAULT '');
		INSERT INTO boxes (name) VALUES ('hackbox1')`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	db, err := openDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	// A second open runs the migration again; the duplicate column is fine.
	db, err = openDB(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE boxes SET applied_config_version = 'x'`); err != nil {
		t.Errorf("column missing: %v", err)
	}
}
