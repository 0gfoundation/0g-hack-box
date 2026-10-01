package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const githubRetry = 5 * 60 // seconds between retries of a failed push

type githubWorker struct {
	s       *Server
	apiBase string
	pushURL string
	gitCmd  string
	client  *http.Client
	wake    chan struct{}
}

func newGitHubWorker(s *Server, apiBase, pushBase, gitCmd string) *githubWorker {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	if pushBase == "" {
		pushBase = "https://github.com"
	}
	if gitCmd == "" {
		gitCmd = "git"
	}
	return &githubWorker{
		s:       s,
		apiBase: strings.TrimRight(apiBase, "/"),
		pushURL: strings.TrimRight(pushBase, "/"),
		gitCmd:  gitCmd,
		client:  &http.Client{Timeout: 30 * time.Second},
		wake:    make(chan struct{}, 1),
	}
}

func (g *githubWorker) enabled() bool { return g.s.cfg.GitHub.Token != "" }

func (g *githubWorker) kick() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// requeueDisabled queues archives stored while no token was configured.
func (g *githubWorker) requeueDisabled() error {
	if !g.enabled() {
		return nil
	}
	_, err := g.s.db.Exec(`UPDATE sessions SET github_status = 'queued' WHERE github_status = 'disabled' AND archive_path != ''`)
	return err
}

func (g *githubWorker) run(ctx context.Context) {
	if !g.enabled() {
		g.s.log.Printf("github: no token in config, pushes disabled")
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		g.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-g.wake:
		}
	}
}

// drain pushes every due session, one at a time.
func (g *githubWorker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		ss, err := g.next()
		if err != nil {
			g.s.log.Printf("github: %v", err)
			return
		}
		if ss == nil {
			return
		}
		g.process(ctx, ss)
	}
}

func (g *githubWorker) next() (*Session, error) {
	now := g.s.now().Unix()
	row := g.s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE archive_path != '' AND
		(github_status = 'queued' OR (github_status = 'failed' AND github_next_at <= ?))
		ORDER BY github_next_at, created_at LIMIT 1`, now)
	ss, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return ss, nil
}

func (g *githubWorker) process(ctx context.Context, ss *Session) {
	repo := g.repoName(ss)
	err := g.push(ctx, ss, repo)
	now := g.s.now().Unix()
	if err != nil {
		msg := g.redact(err.Error())
		if len(msg) > 500 {
			msg = msg[:500]
		}
		g.s.log.Printf("github: session %s repo %s failed: %s", ss.ID, repo, msg)
		g.s.db.Exec(`UPDATE sessions SET github_status = 'failed', github_error = ?, github_repo = ?, github_next_at = ? WHERE id = ?`,
			msg, repo, now+githubRetry, ss.ID)
		return
	}
	g.s.log.Printf("github: session %s pushed to %s/%s", ss.ID, g.s.cfg.GitHub.Org, repo)
	g.s.db.Exec(`UPDATE sessions SET github_status = 'pushed', github_error = '', github_repo = ? WHERE id = ?`, repo, ss.ID)
}

func (g *githubWorker) push(ctx context.Context, ss *Session, repo string) error {
	if err := g.createRepo(ctx, ss, repo); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "hackbox-push-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "work")
	gitDir := filepath.Join(tmp, "repo.git")
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	f, err := os.Open(ss.ArchivePath)
	if err != nil {
		return err
	}
	err = unpackTarGz(f, work)
	f.Close()
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}

	org := g.s.cfg.GitHub.Org
	u, err := url.Parse(g.pushURL)
	if err != nil {
		return err
	}
	u.User = url.UserPassword("x-access-token", g.s.cfg.GitHub.Token)
	u.Path = "/" + org + "/" + repo + ".git"
	remote := u.String()

	when := time.Unix(ss.StartedAt, 0).In(g.s.loc)
	msg := fmt.Sprintf("Session %s on %s, %s", ss.Code, ss.Box, displayName(ss.Name))
	base := []string{"--git-dir=" + gitDir, "--work-tree=" + work}
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=hack-box", "-c", "user.email=hackbox@0g.ai", "-c", "commit.gpgsign=false",
			"commit", "-q", "--allow-empty", "-m", msg},
		{"push", "-q", "--force", remote, "main"},
	}
	for _, step := range steps {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		cmd := exec.CommandContext(cctx, g.gitCmd, append(append([]string{}, base...), step...)...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"HOME="+tmp,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=hack-box", "GIT_AUTHOR_EMAIL=hackbox@0g.ai",
			"GIT_COMMITTER_NAME=hack-box", "GIT_COMMITTER_EMAIL=hackbox@0g.ai",
			"GIT_AUTHOR_DATE="+when.Format(time.RFC3339),
		)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			// The push step's arguments carry the token; never echo them.
			return fmt.Errorf("git %s: %v: %s", stepName(step), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func stepName(step []string) string {
	for _, a := range step {
		switch a {
		case "init", "add", "commit", "push":
			return a
		}
	}
	return "?"
}

func (g *githubWorker) createRepo(ctx context.Context, ss *Session, repo string) error {
	when := time.Unix(ss.StartedAt, 0).In(g.s.loc)
	body, _ := json.Marshal(map[string]any{
		"name":        repo,
		"private":     true,
		"description": fmt.Sprintf("hack-box session %s %s %s", ss.Box, displayName(ss.Name), when.Format("2006-01-02 15:04")),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		g.apiBase+"/orgs/"+url.PathEscape(g.s.cfg.GitHub.Org)+"/repos", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.s.cfg.GitHub.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("create repo: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(string(rb), "name already exists"):
		return nil
	}
	return fmt.Errorf("create repo: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
}

// redact removes the token from any text, raw or URL encoded.
func (g *githubWorker) redact(s string) string {
	tok := g.s.cfg.GitHub.Token
	if tok == "" {
		return s
	}
	s = strings.ReplaceAll(s, tok, "***")
	if e := url.QueryEscape(tok); e != tok {
		s = strings.ReplaceAll(s, e, "***")
	}
	return s
}

func (g *githubWorker) repoName(ss *Session) string {
	if ss.GitHubRepo != "" {
		return ss.GitHubRepo // keep the name across retries and re-uploads
	}
	when := time.Unix(ss.StartedAt, 0).In(g.s.loc)
	return fmt.Sprintf("%s-%s-%s-%s", when.Format("20060102-1504"), slug(ss.Box, 30, "box"), slug(ss.Name, 30, "anon"), ss.Code)
}

// slug keeps lowercase ascii letters and digits, joins the rest with single
// dashes, and cuts to max characters.
func slug(s string, max int, fallback string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			dash = false
		} else if !dash && sb.Len() > 0 {
			sb.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(sb.String(), "-")
	if len(out) > max {
		out = strings.TrimRight(out[:max], "-")
	}
	if out == "" {
		return fallback
	}
	return out
}

func displayName(n string) string {
	if strings.TrimSpace(n) == "" {
		return "anonymous"
	}
	return n
}
