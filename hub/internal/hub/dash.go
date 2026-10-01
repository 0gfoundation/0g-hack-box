package hub

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

// onlineWindow: a box is online when its last heartbeat is this recent.
const onlineWindow = 15

func (s *Server) dashPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write(dashboardHTML)
}

func (s *Server) dashAPITest(w http.ResponseWriter, r *http.Request) {
	if len(s.apiTest) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.apiTest)
}

type dashRequest struct {
	ID      int64  `json:"id"`
	AskedAt int64  `json:"asked_at"`
	Status  string `json:"status"`
	Minutes int    `json:"minutes"`
}

type dashBox struct {
	Name            string       `json:"name"`
	Online          bool         `json:"online"`
	LastSeen        int64        `json:"last_seen"`
	State           string       `json:"state"`
	SecondsLeft     int64        `json:"seconds_left"`
	SessionID       string       `json:"session_id"`
	Attendee        string       `json:"attendee"`
	Agent           string       `json:"agent"`
	StartedAt       int64        `json:"started_at"`
	Minutes         int          `json:"minutes"`
	ExtendedMinutes int          `json:"extended_minutes"`
	Code            string       `json:"code"`
	Request         *dashRequest `json:"request"`
	// ConfigVersion is what the hub wants, AppliedConfigVersion what the box
	// last reported. Equal means the keys on the box are up to date.
	ConfigVersion        string `json:"config_version"`
	AppliedConfigVersion string `json:"applied_config_version"`
	ConfigUpToDate       bool   `json:"config_up_to_date"`
	// Activity: friendly names of the programs open on the box, [] when idle.
	Activity []string `json:"activity"`
	// Enrolled boxes got their name from POST /api/v1/enroll.
	Enrolled   bool            `json:"enrolled"`
	MAC        string          `json:"mac"`
	EnrolledAt int64           `json:"enrolled_at"`
	Status     json.RawMessage `json:"status"`
}

type dashSession struct {
	ID              string `json:"id"`
	Box             string `json:"box"`
	Name            string `json:"name"`
	Agent           string `json:"agent"`
	Code            string `json:"code"`
	LocalCode       string `json:"local_code"`
	Minutes         int    `json:"minutes"`
	ExtendedMinutes int    `json:"extended_minutes"`
	StartedAt       int64  `json:"started_at"`
	EndedAt         int64  `json:"ended_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	EndReason       string `json:"end_reason"`
	ArchiveBytes    int64  `json:"archive_bytes"`
	Empty           bool   `json:"empty"`
	GitHubStatus    string `json:"github_status"`
	GitHubRepo      string `json:"github_repo"`
	GitHubURL       string `json:"github_url"`
	GitHubError     string `json:"github_error"`
	DownloadURL     string `json:"download_url"`
	PublicURL       string `json:"public_url"`
	ExpiresAt       int64  `json:"expires_at"`
}

type dashStateResp struct {
	Now      int64         `json:"now"`
	Boxes    []dashBox     `json:"boxes"`
	Sessions []dashSession `json:"sessions"`
}

func (s *Server) dashState(w http.ResponseWriter, r *http.Request) {
	now := s.now().Unix()
	resp := dashStateResp{Now: now, Boxes: []dashBox{}, Sessions: []dashSession{}}

	boxes := map[string]*dashBox{}
	for _, n := range s.allBoxNames() {
		boxes[n] = &dashBox{Name: n, Status: json.RawMessage("{}"), Activity: []string{}}
	}
	rows, err := s.db.Query(`SELECT name, last_seen, state, seconds_left, status_json, session_id,
		applied_config_version, activity_json FROM boxes`)
	if err != nil {
		s.serverError(w, err)
		return
	}
	for rows.Next() {
		var b dashBox
		var st, act string
		if err := rows.Scan(&b.Name, &b.LastSeen, &b.State, &b.SecondsLeft, &st, &b.SessionID, &b.AppliedConfigVersion, &act); err != nil {
			rows.Close()
			s.serverError(w, err)
			return
		}
		b.Status = json.RawMessage(st)
		if !json.Valid(b.Status) {
			b.Status = json.RawMessage("{}")
		}
		if json.Unmarshal([]byte(act), &b.Activity) != nil || b.Activity == nil {
			b.Activity = []string{}
		}
		b.Online = now-b.LastSeen <= onlineWindow
		bb := b
		boxes[b.Name] = &bb
	}
	rows.Close()
	names := make([]string, 0, len(boxes))
	for n := range boxes {
		names = append(names, n)
	}
	versions, err := s.boxConfigVersions(names)
	if err != nil {
		s.serverError(w, err)
		return
	}
	enr, err := s.enrollments()
	if err != nil {
		s.serverError(w, err)
		return
	}
	for _, b := range boxes {
		if e, ok := enr[b.Name]; ok {
			b.Enrolled, b.MAC, b.EnrolledAt = true, e.MAC, e.At
		}
		b.ConfigVersion = versions[b.Name]
		b.ConfigUpToDate = b.ConfigVersion == b.AppliedConfigVersion
		if b.SessionID != "" {
			if ss, err := s.sessionBy("id", b.SessionID); err == nil {
				b.Attendee, b.Agent, b.StartedAt = ss.Name, ss.Agent, ss.StartedAt
				b.Minutes, b.ExtendedMinutes, b.Code = ss.Minutes, ss.ExtendedMinutes, ss.Code
			}
			var q dashRequest
			err := s.db.QueryRow(`SELECT id, asked_at, status, minutes FROM requests
				WHERE session_id = ? AND status = 'pending' ORDER BY id DESC LIMIT 1`, b.SessionID).
				Scan(&q.ID, &q.AskedAt, &q.Status, &q.Minutes)
			if err == nil {
				b.Request = &q
			}
		}
		resp.Boxes = append(resp.Boxes, *b)
	}
	sort.Slice(resp.Boxes, func(i, j int) bool { return naturalLess(resp.Boxes[i].Name, resp.Boxes[j].Name) })

	list, err := s.recentSessions(200)
	if err != nil {
		s.serverError(w, err)
		return
	}
	for _, ss := range list {
		d := dashSession{
			ID: ss.ID, Box: ss.Box, Name: ss.Name, Agent: ss.Agent, Code: ss.Code, LocalCode: ss.LocalCode,
			Minutes: ss.Minutes, ExtendedMinutes: ss.ExtendedMinutes, StartedAt: ss.StartedAt,
			EndedAt: ss.EndedAt, EndReason: ss.EndReason, ArchiveBytes: ss.ArchiveBytes, Empty: ss.Empty,
			GitHubStatus: ss.GitHubStatus, GitHubRepo: ss.GitHubRepo, GitHubError: ss.GitHubError,
			PublicURL: s.downloadURL(ss.Token), ExpiresAt: ss.ExpiresAt,
		}
		if ss.EndedAt > ss.StartedAt {
			d.DurationSeconds = ss.EndedAt - ss.StartedAt
		}
		if ss.GitHubStatus == "pushed" && ss.GitHubRepo != "" {
			d.GitHubURL = "https://github.com/" + s.cfg.GitHub.Org + "/" + ss.GitHubRepo
		}
		if ss.ArchivePath != "" {
			d.DownloadURL = "/dash/sessions/" + ss.ID + "/download"
		}
		resp.Sessions = append(resp.Sessions, d)
	}
	writeJSON(w, http.StatusOK, resp)
}

// naturalLess sorts hackbox2 before hackbox10.
func naturalLess(a, b string) bool {
	pa, na := splitNum(a)
	pb, nb := splitNum(b)
	if pa != pb {
		return pa < pb
	}
	return na < nb
}

func splitNum(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(s[i:])
	return s[:i], n
}

func (s *Server) knownBox(name string) bool {
	for _, n := range s.allBoxNames() {
		if n == name {
			return true
		}
	}
	return false
}

// queueCommand stores a command for the box's current session.
func (s *Server) queueCommand(box, op string, minutes int) (int64, error) {
	var sid string
	err := s.db.QueryRow(`SELECT session_id FROM boxes WHERE name = ?`, box).Scan(&sid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO commands (box, session_id, op, minutes, created_at) VALUES (?,?,?,?,?)`,
		box, sid, op, minutes, s.now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type minutesReq struct {
	Minutes int `json:"minutes"`
}

func (s *Server) dashExtend(w http.ResponseWriter, r *http.Request) {
	box := r.PathValue("box")
	if !s.knownBox(box) {
		jsonError(w, http.StatusNotFound, "unknown box")
		return
	}
	var req minutesReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Minutes < 1 || req.Minutes > 60 {
		jsonError(w, http.StatusBadRequest, "minutes must be 1..60")
		return
	}
	id, err := s.queueCommand(box, "extend", req.Minutes)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command_id": strconv.FormatInt(id, 10)})
}

func (s *Server) dashEnd(w http.ResponseWriter, r *http.Request) {
	box := r.PathValue("box")
	if !s.knownBox(box) {
		jsonError(w, http.StatusNotFound, "unknown box")
		return
	}
	id, err := s.queueCommand(box, "end", 0)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command_id": strconv.FormatInt(id, 10)})
}

type decideReq struct {
	Approve flexBool `json:"approve"`
	Minutes int      `json:"minutes"`
}

func (s *Server) dashDecide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusNotFound, "no such request")
		return
	}
	var req decideReq
	if !readJSON(w, r, &req) {
		return
	}
	var box, status, sid string
	err = s.db.QueryRow(`SELECT box, status, session_id FROM requests WHERE id = ?`, id).Scan(&box, &status, &sid)
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "no such request")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	if status != "pending" {
		jsonError(w, http.StatusConflict, "request is already "+status)
		return
	}
	now := s.now().Unix()
	resp := map[string]any{}
	if req.Approve {
		if req.Minutes == 0 {
			req.Minutes = 5
		}
		if req.Minutes < 1 || req.Minutes > 60 {
			jsonError(w, http.StatusBadRequest, "minutes must be 1..60")
			return
		}
		var cur string
		s.db.QueryRow(`SELECT session_id FROM boxes WHERE name = ?`, box).Scan(&cur)
		if cur != sid {
			jsonError(w, http.StatusConflict, "that session is no longer running on "+box)
			return
		}
		cid, err := s.queueCommand(box, "extend", req.Minutes)
		if err != nil {
			s.serverError(w, err)
			return
		}
		resp["command_id"] = strconv.FormatInt(cid, 10)
		_, err = s.db.Exec(`UPDATE requests SET status = 'approved', minutes = ?, decided_at = ? WHERE id = ?`, req.Minutes, now, id)
		if err != nil {
			s.serverError(w, err)
			return
		}
	} else {
		if _, err := s.db.Exec(`UPDATE requests SET status = 'declined', decided_at = ? WHERE id = ?`, now, id); err != nil {
			s.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dashDownload(w http.ResponseWriter, r *http.Request) {
	ss, err := s.sessionBy("id", r.PathValue("id"))
	if errors.Is(err, errNotFound) || (err == nil && ss.ArchivePath == "") {
		http.Error(w, "no archive for that session", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.streamZip(w, ss)
}
