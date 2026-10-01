package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// flexID accepts a JSON string, number or null, so a box agent that echoes an
// id back in either form still works.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexID(n.String())
	return nil
}

// flexBool accepts true/false, 0/1 and "true"/"false".
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		*f = true
	case "false", "0", "no", "", "null":
		*f = false
	default:
		return fmt.Errorf("not a boolean: %s", s)
	}
	return nil
}

type createSessionReq struct {
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Minutes   int    `json:"minutes"`
	StartedAt int64  `json:"started_at"`
	LocalCode string `json:"local_code"`
}

type createSessionResp struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Server) downloadURL(token string) string {
	return s.cfg.PublicBaseURL + "/d/" + token
}

func (s *Server) apiCreateSession(w http.ResponseWriter, r *http.Request, box string) {
	var req createSessionReq
	if !readJSON(w, r, &req) {
		return
	}
	now := s.now().Unix()
	if req.StartedAt <= 0 {
		req.StartedAt = now
	}
	if req.Minutes < 0 || req.Minutes > 24*60 {
		jsonError(w, http.StatusBadRequest, "minutes out of range")
		return
	}
	base := req.StartedAt
	if now > base {
		base = now
	}
	expires := base + int64(s.cfg.DownloadDays)*86400

	var code string
	for i := 0; ; i++ {
		if i == 50 {
			jsonError(w, http.StatusServiceUnavailable, "could not make a unique code")
			return
		}
		code = randomCode()
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE code = ? AND expires_at > ?`, code, now).Scan(&n); err != nil {
			s.serverError(w, err)
			return
		}
		if n == 0 {
			break
		}
	}
	id := randomID()
	token := randomToken()
	ghStatus := "disabled"
	_, err := s.db.Exec(`INSERT INTO sessions (id, box, name, agent, minutes, started_at, token, code,
		local_code, expires_at, github_status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, box, cleanText(req.Name, 80), cleanText(req.Agent, 40), req.Minutes, req.StartedAt,
		token, code, cleanText(req.LocalCode, 16), expires, ghStatus, now)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.db.Exec(`INSERT INTO boxes (name, session_id) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET session_id = excluded.session_id`, box, id)
	writeJSON(w, http.StatusOK, createSessionResp{ID: id, Token: token, Code: code, URL: s.downloadURL(token), ExpiresAt: expires})
}

// ownedSession loads {id} and checks it belongs to the calling box.
func (s *Server) ownedSession(w http.ResponseWriter, r *http.Request, box string) *Session {
	ss, err := s.sessionBy("id", r.PathValue("id"))
	if errors.Is(err, errNotFound) || (err == nil && ss.Box != box) {
		jsonError(w, http.StatusNotFound, "no such session for this box")
		return nil
	}
	if err != nil {
		s.serverError(w, err)
		return nil
	}
	return ss
}

func (s *Server) apiQR(w http.ResponseWriter, r *http.Request, box string) {
	ss := s.ownedSession(w, r, box)
	if ss == nil {
		return
	}
	png, err := qrcode.Encode(s.downloadURL(ss.Token), qrcode.Medium, 512)
	if err != nil {
		s.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

type heartbeatReq struct {
	SessionID       flexID          `json:"session_id"`
	State           string          `json:"state"`
	SecondsLeft     int64           `json:"seconds_left"`
	ExtendRequestAt int64           `json:"extend_request_at"`
	Status          json.RawMessage `json:"status"`
	// AppliedConfigVersion is the config version the box last applied.
	// Omitted means "unchanged".
	AppliedConfigVersion *string `json:"applied_config_version"`
	// Activity is the friendly names of the programs the attendee runs.
	// Invalid entries are dropped; it is cleared when the box is idle.
	Activity json.RawMessage `json:"activity"`
}

type commandOut struct {
	ID      string `json:"id"`
	Op      string `json:"op"`
	Minutes int    `json:"minutes"`
}

type extendOut struct {
	Status  string `json:"status"`
	Minutes int    `json:"minutes"`
}

type heartbeatResp struct {
	Commands []commandOut `json:"commands"`
	Extend   extendOut    `json:"extend"`
	// ConfigVersion is the agent config the hub wants on this box; "" when none is set.
	ConfigVersion string `json:"config_version"`
}

// commandTTL: a command the box has not picked up by then is dropped, so a
// stale "end" never hits the next attendee's session.
const commandTTL = 120

func (s *Server) apiHeartbeat(w http.ResponseWriter, r *http.Request, box string) {
	var req heartbeatReq
	if !readJSON(w, r, &req) {
		return
	}
	now := s.now().Unix()
	sid := string(req.SessionID)
	if sid != "" {
		ss, err := s.sessionBy("id", sid)
		if err != nil || ss.Box != box {
			sid = "" // not ours or unknown: treat as no session
		}
	}
	status := "{}"
	if len(req.Status) > 0 && json.Valid(req.Status) && len(req.Status) < 64<<10 {
		status = string(req.Status)
	}
	state := cleanText(req.State, 20)
	activity := cleanActivity(req.Activity)
	if state == "idle" {
		activity = []string{}
	}
	actJSON, _ := json.Marshal(activity)
	_, err := s.db.Exec(`INSERT INTO boxes (name, last_seen, state, seconds_left, status_json, session_id, activity_json)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET last_seen=excluded.last_seen,
		state=excluded.state, seconds_left=excluded.seconds_left, status_json=excluded.status_json,
		session_id=excluded.session_id, activity_json=excluded.activity_json`,
		box, now, state, req.SecondsLeft, status, sid, string(actJSON))
	if err != nil {
		s.serverError(w, err)
		return
	}
	if req.AppliedConfigVersion != nil {
		if _, err := s.db.Exec(`UPDATE boxes SET applied_config_version = ? WHERE name = ?`,
			cleanText(*req.AppliedConfigVersion, 64), box); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if sid != "" && req.ExtendRequestAt != 0 {
		_, err := s.db.Exec(`INSERT INTO requests (session_id, box, asked_at, status) VALUES (?,?,?,'pending')
			ON CONFLICT(session_id, asked_at) DO NOTHING`, sid, box, req.ExtendRequestAt)
		if err != nil {
			s.serverError(w, err)
			return
		}
	}

	resp := heartbeatResp{Commands: []commandOut{}, Extend: extendOut{Status: "none"}}
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE commands SET picked_at = ?, ok = 0, message = 'expired: box did not pick it up', done_at = ?
		WHERE box = ? AND picked_at = 0 AND created_at < ?`, now, now, box, now-commandTTL); err != nil {
		s.serverError(w, err)
		return
	}
	rows, err := tx.Query(`SELECT id, op, minutes FROM commands WHERE box = ? AND picked_at = 0 ORDER BY id`, box)
	if err != nil {
		s.serverError(w, err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var c commandOut
		if err := rows.Scan(&id, &c.Op, &c.Minutes); err != nil {
			rows.Close()
			s.serverError(w, err)
			return
		}
		c.ID = strconv.FormatInt(id, 10)
		ids = append(ids, id)
		resp.Commands = append(resp.Commands, c)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE commands SET picked_at = ? WHERE id = ?`, now, id); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, err)
		return
	}
	if sid != "" {
		err := s.db.QueryRow(`SELECT status, minutes FROM requests WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sid).
			Scan(&resp.Extend.Status, &resp.Extend.Minutes)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.serverError(w, err)
			return
		}
	}
	eff, err := s.effectiveConfig(box)
	if err != nil {
		s.serverError(w, err)
		return
	}
	resp.ConfigVersion = configVersion(eff)
	writeJSON(w, http.StatusOK, resp)
}

type commandResultReq struct {
	OK      flexBool `json:"ok"`
	Message string   `json:"message"`
}

func (s *Server) apiCommandResult(w http.ResponseWriter, r *http.Request, box string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusNotFound, "no such command")
		return
	}
	var req commandResultReq
	if !readJSON(w, r, &req) {
		return
	}
	var cbox, op, sid string
	var minutes int
	var doneAt int64
	err = s.db.QueryRow(`SELECT box, op, minutes, session_id, done_at FROM commands WHERE id = ?`, id).
		Scan(&cbox, &op, &minutes, &sid, &doneAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && cbox != box) {
		jsonError(w, http.StatusNotFound, "no such command for this box")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE commands SET ok = ?, message = ?, done_at = ? WHERE id = ?`,
		b2i(bool(req.OK)), cleanText(req.Message, 500), now, id); err != nil {
		s.serverError(w, err)
		return
	}
	// Count an extension once, even if the box reports the result twice.
	if op == "extend" && bool(req.OK) && doneAt == 0 && sid != "" {
		if _, err := tx.Exec(`UPDATE sessions SET extended_minutes = extended_minutes + ? WHERE id = ?`, minutes, sid); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

type endReq struct {
	EndedAt int64    `json:"ended_at"`
	Reason  string   `json:"reason"`
	Empty   flexBool `json:"empty"`
}

func (s *Server) apiEnd(w http.ResponseWriter, r *http.Request, box string) {
	ss := s.ownedSession(w, r, box)
	if ss == nil {
		return
	}
	var req endReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.EndedAt <= 0 {
		req.EndedAt = s.now().Unix()
	}
	expires := ss.ExpiresAt
	if e := req.EndedAt + int64(s.cfg.DownloadDays)*86400; e > expires {
		expires = e
	}
	// An archive that already arrived wins over a late "empty".
	empty := bool(req.Empty) && ss.ArchivePath == ""
	if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ?, end_reason = ?, empty = ?, expires_at = ? WHERE id = ?`,
		req.EndedAt, cleanText(req.Reason, 60), b2i(empty), expires, ss.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.db.Exec(`UPDATE requests SET status = 'declined', decided_at = ? WHERE session_id = ? AND status = 'pending'`,
		s.now().Unix(), ss.ID)
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) apiArchive(w http.ResponseWriter, r *http.Request, box string) {
	ss := s.ownedSession(w, r, box)
	if ss == nil {
		return
	}
	tmp, err := os.CreateTemp(s.archiveDir, ".upload-*.part")
	if err != nil {
		s.serverError(w, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after the rename
	n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, s.maxArchive))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("archive is larger than %d bytes", s.maxArchive))
			return
		}
		jsonError(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return
	}
	if err := checkTarGz(tmpName); err != nil {
		jsonError(w, http.StatusBadRequest, "not a valid .tar.gz: "+err.Error())
		return
	}
	final := filepath.Join(s.archiveDir, ss.ID+".tar.gz")
	if err := os.Chmod(tmpName, 0o600); err != nil {
		s.serverError(w, err)
		return
	}
	if err := os.Rename(tmpName, final); err != nil {
		s.serverError(w, err)
		return
	}
	gh := "disabled"
	if s.cfg.GitHub.Token != "" {
		gh = "queued"
	}
	if _, err := s.db.Exec(`UPDATE sessions SET archive_path = ?, archive_bytes = ?, empty = 0,
		github_status = ?, github_error = '', github_next_at = 0 WHERE id = ?`, final, n, gh, ss.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.gh.kick()
	writeJSON(w, http.StatusOK, struct{}{})
}

// checkTarGz reads the whole archive once through gzip and tar.
func checkTarGz(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return err
		}
	}
	_, err = io.Copy(io.Discard, gz)
	return err
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.log.Printf("error: %v", err)
	jsonError(w, http.StatusInternalServerError, "internal error")
}
