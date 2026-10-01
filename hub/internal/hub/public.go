package hub

import (
	"errors"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"
)

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
{{if .Refresh}}<meta http-equiv="refresh" content="{{.Refresh}}">{{end}}
<title>{{.Title}}</title>
<style>
:root{--bg:#0b0d12;--card:#141821;--line:#232a36;--text:#e8ecf3;--muted:#8b95a7;--accent:#9b7bff;--accent2:#6d4dff;--ok:#38d39f;--warn:#ffb547;--bad:#ff6b6b}
*{box-sizing:border-box}
html,body{margin:0;background:var(--bg);color:var(--text);font:16px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
main{max-width:480px;margin:0 auto;padding:40px 20px 56px}
.brand{display:flex;align-items:center;gap:10px;color:var(--muted);font-weight:600;letter-spacing:.02em;margin-bottom:28px}
.logo{width:34px;height:34px;border-radius:9px;background:linear-gradient(135deg,var(--accent),var(--accent2));display:grid;place-items:center;color:#fff;font-weight:800;font-size:15px}
.card{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:28px 24px}
h1{font-size:26px;line-height:1.25;margin:0 0 8px}
p{margin:0 0 14px;color:var(--muted)}
.meta{display:grid;grid-template-columns:auto 1fr;gap:6px 16px;margin:20px 0 24px;font-size:15px}
.meta dt{color:var(--muted)}.meta dd{margin:0}
.btn{display:block;width:100%;text-align:center;padding:18px;border-radius:14px;border:0;background:linear-gradient(135deg,var(--accent),var(--accent2));color:#fff;font-size:19px;font-weight:700;text-decoration:none;cursor:pointer}
.btn:active{transform:translateY(1px)}
.size{text-align:center;color:var(--muted);font-size:14px;margin-top:10px}
.spin{width:44px;height:44px;border-radius:50%;border:4px solid var(--line);border-top-color:var(--accent);animation:s 1s linear infinite;margin:6px 0 20px}
@keyframes s{to{transform:rotate(360deg)}}
.pill{display:inline-block;padding:3px 10px;border-radius:999px;font-size:13px;font-weight:600;margin-bottom:14px}
.pill.ok{background:rgba(56,211,159,.12);color:var(--ok)}.pill.warn{background:rgba(255,181,71,.12);color:var(--warn)}.pill.bad{background:rgba(255,107,107,.12);color:var(--bad)}
input[type=text]{width:100%;font:600 28px/1 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.3em;text-transform:uppercase;text-align:center;padding:16px 10px;border-radius:14px;border:1px solid var(--line);background:#0e1118;color:var(--text);margin:8px 0 16px}
input[type=text]:focus{outline:2px solid var(--accent);border-color:transparent}
.err{color:var(--bad);font-size:15px;margin:0 0 12px}
.foot{margin-top:22px;text-align:center;font-size:13px;color:var(--muted)}
.foot a{color:var(--muted)}
</style>
</head><body><main>
<div class="brand"><div class="logo">0G</div>0G hack-box</div>
<div class="card">
{{if eq .State "preparing"}}
  <span class="pill warn">Preparing</span>
  <h1>Hi {{.First}}, preparing your files...</h1>
  <div class="spin" aria-hidden="true"></div>
  <p>Your project is being saved from the box. This page updates by itself, keep it open.</p>
  {{template "meta" .}}
{{else if eq .State "ready"}}
  <span class="pill ok">Ready</span>
  <h1>Hi {{.First}}, your project is ready</h1>
  <p>Everything from <code>~/project</code> on the box, as a zip.</p>
  {{template "meta" .}}
  <a class="btn" href="/d/{{.Token}}/download">Download</a>
  <div class="size">{{.Size}} compressed. Link works until {{.Expires}}.</div>
{{else if eq .State "empty"}}
  <span class="pill warn">Nothing saved</span>
  <h1>Hi {{.First}}, there was nothing to save</h1>
  <p>There was nothing new in ~/project to save.</p>
  {{template "meta" .}}
{{else if eq .State "expired"}}
  <span class="pill bad">Expired</span>
  <h1>This link has expired</h1>
  <p>Downloads are kept for {{.Days}} days. Ask the hack-box staff if you still need your files.</p>
{{else if eq .State "notfound"}}
  <span class="pill bad">Not found</span>
  <h1>We could not find that session</h1>
  <p>Check the link, or type your 6 character code instead.</p>
  <a class="btn" href="/code">Enter a code</a>
{{else if eq .State "code"}}
  <h1>Get your project</h1>
  <p>Type the 6 character code from the hack-box screen.</p>
  <form method="post" action="/code" autocomplete="off">
    <input type="text" name="code" maxlength="12" inputmode="text" autocapitalize="characters" spellcheck="false" placeholder="K7MZQ2" value="{{.Code}}" autofocus>
    {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
    <button class="btn" type="submit">Find my files</button>
  </form>
{{else if eq .State "slow"}}
  <span class="pill warn">Slow down</span>
  <h1>Too many tries</h1>
  <p>Please wait a minute, then try your code again.</p>
  <a class="btn" href="/code">Try again</a>
{{end}}
</div>
<div class="foot">0G hack-box{{if ne .State "code"}} · <a href="/code">enter a code</a>{{end}}</div>
</main></body></html>
{{define "meta"}}<dl class="meta">
  <dt>Name</dt><dd>{{.FullName}}</dd>
  <dt>Box</dt><dd>{{.Box}}</dd>
  <dt>Session</dt><dd>{{.Date}}</dd>
</dl>{{end}}`))

type pageData struct {
	Title    string
	State    string
	Refresh  int
	First    string
	FullName string
	Box      string
	Date     string
	Token    string
	Size     string
	Expires  string
	Days     int
	Code     string
	Error    string
}

func (s *Server) renderPage(w http.ResponseWriter, code int, d pageData) {
	if d.Title == "" {
		d.Title = "0G hack-box"
	}
	d.Days = s.cfg.DownloadDays
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(code)
	if err := pageTmpl.Execute(w, d); err != nil {
		s.log.Printf("template: %v", err)
	}
}

func firstName(n string) string {
	f := strings.Fields(n)
	if len(f) == 0 {
		return "there"
	}
	return f[0]
}

// sessionState is what the attendee page shows.
func (s *Server) sessionState(ss *Session) string {
	switch {
	case s.now().Unix() >= ss.ExpiresAt:
		return "expired"
	case ss.ArchivePath != "":
		return "ready"
	case ss.Empty && ss.EndedAt != 0:
		return "empty"
	}
	return "preparing"
}

func (s *Server) pageDownload(w http.ResponseWriter, r *http.Request) {
	ss, err := s.sessionBy("token", r.PathValue("token"))
	if errors.Is(err, errNotFound) {
		s.renderPage(w, http.StatusNotFound, pageData{State: "notfound", Title: "Not found"})
		return
	}
	if err != nil {
		s.log.Printf("error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	st := s.sessionState(ss)
	d := pageData{
		State:    st,
		First:    firstName(ss.Name),
		FullName: displayName(ss.Name),
		Box:      ss.Box,
		Date:     time.Unix(ss.StartedAt, 0).In(s.loc).Format("Mon 2 Jan 2006, 15:04"),
		Token:    ss.Token,
		Size:     humanBytes(ss.ArchiveBytes),
		Expires:  time.Unix(ss.ExpiresAt, 0).In(s.loc).Format("Mon 2 Jan 2006"),
	}
	code := http.StatusOK
	switch st {
	case "expired":
		code = http.StatusGone
	case "preparing":
		d.Refresh = 3
	}
	s.renderPage(w, code, d)
}

func (s *Server) pageDownloadZip(w http.ResponseWriter, r *http.Request) {
	ss, err := s.sessionBy("token", r.PathValue("token"))
	if errors.Is(err, errNotFound) {
		s.renderPage(w, http.StatusNotFound, pageData{State: "notfound", Title: "Not found"})
		return
	}
	if err != nil {
		s.log.Printf("error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch s.sessionState(ss) {
	case "expired":
		s.renderPage(w, http.StatusGone, pageData{State: "expired", Title: "Expired"})
		return
	case "ready":
		s.streamZip(w, ss)
	default:
		http.Redirect(w, r, "/d/"+ss.Token, http.StatusSeeOther)
	}
}

// streamZip sends the session's archive as hackbox-<code>.zip.
func (s *Server) streamZip(w http.ResponseWriter, ss *Session) {
	f, err := os.Open(ss.ArchivePath)
	if err != nil {
		s.log.Printf("error: open archive %s: %v", ss.ID, err)
		http.Error(w, "archive missing", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="hackbox-`+ss.Code+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := tarGzToZip(f, w, ss.Code+"-project/"); err != nil {
		// Headers are gone; the client sees a truncated zip.
		s.log.Printf("error: zip for session %s: %v", ss.ID, err)
	}
}

func (s *Server) pageCode(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, http.StatusOK, pageData{State: "code", Title: "Get your project"})
}

func (s *Server) pageCodePost(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		s.renderPage(w, http.StatusTooManyRequests, pageData{State: "slow", Title: "Too many tries"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	raw := r.PostFormValue("code")
	code := normalizeCode(raw)
	d := pageData{State: "code", Title: "Get your project", Code: cleanText(raw, 12)}
	if len(code) != 6 {
		d.Error = "Codes are 6 characters, letters and digits."
		s.renderPage(w, http.StatusBadRequest, d)
		return
	}
	var token string
	err := s.db.QueryRow(`SELECT token FROM sessions WHERE code = ? AND expires_at > ? ORDER BY created_at DESC LIMIT 1`,
		code, s.now().Unix()).Scan(&token)
	if err != nil {
		d.Error = "No session with that code. Check it and try again."
		s.renderPage(w, http.StatusNotFound, d)
		return
	}
	http.Redirect(w, r, "/d/"+token, http.StatusSeeOther)
}
