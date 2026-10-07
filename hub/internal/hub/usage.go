package hub

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Token usage of the attendee's AI agent (OpenCode), reported by the box agent in the
// heartbeat and in POST /sessions/{id}/end as "usage". The box sums the assistant messages
// of a copy of the attendee's OpenCode database, the same way hub/deploy/token-usage.sh
// does. The home is wiped at the end of a session, so the hub keeps the highest value seen
// of every counter: a smaller report (a restarted count, a partial copy) never lowers it.

// usageMax caps one counter; usageMaxModels caps the per model map.
const (
	usageMax       = int64(1e15)
	usageMaxModels = 32
	usageMaxBytes  = 16 << 10
)

// Usage is one session's token totals. Models maps an OpenCode model id to that model's
// input + output + reasoning tokens.
type Usage struct {
	Input      int64            `json:"input"`
	Output     int64            `json:"output"`
	Reasoning  int64            `json:"reasoning"`
	CacheRead  int64            `json:"cache_read"`
	CacheWrite int64            `json:"cache_write"`
	Replies    int64            `json:"replies"`
	Models     map[string]int64 `json:"models"`
	UpdatedAt  int64            `json:"updated_at"`
}

func usageCount(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, true
	}
	if raw[0] < '0' || raw[0] > '9' { // json.Number would also take "7"
		return 0, false
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	v, err := n.Int64()
	if err != nil || v < 0 || v > usageMax {
		return 0, false
	}
	return v, true
}

// parseUsage validates a reported usage object. Anything malformed gives nil, never an
// error: usage is metrics, it must not fail the heartbeat.
func parseUsage(raw json.RawMessage) *Usage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || len(raw) > usageMaxBytes {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	u := &Usage{Models: map[string]int64{}}
	for k, dst := range map[string]*int64{"input": &u.Input, "output": &u.Output, "reasoning": &u.Reasoning,
		"cache_read": &u.CacheRead, "cache_write": &u.CacheWrite, "replies": &u.Replies} {
		v, ok := usageCount(m[k])
		if !ok {
			return nil
		}
		*dst = v
	}
	if mr := m["models"]; len(mr) > 0 && string(mr) != "null" {
		var models map[string]json.RawMessage
		if json.Unmarshal(mr, &models) != nil {
			return nil
		}
		names := make([]string, 0, len(models))
		for k := range models {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v, ok := usageCount(models[k])
			if !ok {
				return nil
			}
			name := cleanText(k, 80)
			if name == "" {
				name = "?"
			}
			if _, seen := u.Models[name]; !seen && len(u.Models) >= usageMaxModels {
				continue
			}
			u.Models[name] = max(u.Models[name], v)
		}
	}
	return u
}

// saveUsage merges u into the session's row, keeping the maximum of every counter.
func (s *Server) saveUsage(sessionID string, u *Usage) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mj string
	err = tx.QueryRow(`SELECT models_json FROM token_usage WHERE session_id = ?`, sessionID).Scan(&mj)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	models := map[string]int64{}
	json.Unmarshal([]byte(mj), &models)
	for k, v := range u.Models {
		if _, seen := models[k]; !seen && len(models) >= usageMaxModels {
			continue
		}
		models[k] = max(models[k], v)
	}
	merged, _ := json.Marshal(models)
	_, err = tx.Exec(`INSERT INTO token_usage (session_id, input, output, reasoning, cache_read, cache_write,
		replies, models_json, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET input = MAX(input, excluded.input),
		output = MAX(output, excluded.output), reasoning = MAX(reasoning, excluded.reasoning),
		cache_read = MAX(cache_read, excluded.cache_read), cache_write = MAX(cache_write, excluded.cache_write),
		replies = MAX(replies, excluded.replies), models_json = excluded.models_json,
		updated_at = excluded.updated_at`,
		sessionID, u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite, u.Replies, string(merged), s.now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// recordUsage stores a reported usage for an owned session; errors are logged only.
func (s *Server) recordUsage(sessionID string, raw json.RawMessage) {
	if sessionID == "" || len(raw) == 0 {
		return
	}
	u := parseUsage(raw)
	if u == nil {
		return
	}
	if err := s.saveUsage(sessionID, u); err != nil {
		s.log.Printf("error: save usage of %s: %v", sessionID, err)
	}
}

// allUsage returns every session's usage by session id.
func (s *Server) allUsage() (map[string]*Usage, error) {
	rows, err := s.db.Query(`SELECT session_id, input, output, reasoning, cache_read, cache_write, replies,
		models_json, updated_at FROM token_usage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*Usage{}
	for rows.Next() {
		var id, mj string
		u := &Usage{}
		if err := rows.Scan(&id, &u.Input, &u.Output, &u.Reasoning, &u.CacheRead, &u.CacheWrite, &u.Replies,
			&mj, &u.UpdatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(mj), &u.Models) != nil || u.Models == nil {
			u.Models = map[string]int64{}
		}
		out[id] = u
	}
	return out, rows.Err()
}

// UsageTotals adds up every session's usage; Sessions counts those with at least one reply.
type UsageTotals struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Replies    int64 `json:"replies"`
	Sessions   int   `json:"sessions"`
}

func usageTotals(all map[string]*Usage) UsageTotals {
	var t UsageTotals
	for _, u := range all {
		t.Input += u.Input
		t.Output += u.Output
		t.Reasoning += u.Reasoning
		t.CacheRead += u.CacheRead
		t.CacheWrite += u.CacheWrite
		t.Replies += u.Replies
		if u.Replies > 0 {
			t.Sessions++
		}
	}
	return t
}

// ---------------------------------------------------------------- fundings and rewards

type dashFunding struct {
	ID          int64  `json:"id"`
	SessionID   string `json:"session_id"`
	Box         string `json:"box"`
	Attendee    string `json:"attendee"`
	Email       string `json:"email"`
	Telegram    string `json:"telegram"`
	Wallet      string `json:"wallet"`
	AmountMilli int64  `json:"amount_milli"`
	AmountOG    string `json:"amount_og"`
	Status      string `json:"status"`  // queued, executing, completed, failed, refused, unknown
	Counted     bool   `json:"counted"` // uses up the budget (sent, on its way, or maybe sent)
	TxHash      string `json:"tx_hash"`
	TxURL       string `json:"tx_url"`
	Detail      string `json:"detail"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type dashReward struct {
	SessionID    string `json:"session_id"`
	Box          string `json:"box"`
	Attendee     string `json:"attendee"`
	Email        string `json:"email"`
	Telegram     string `json:"telegram"`
	Wallet       string `json:"wallet"`
	AmountMicros int64  `json:"amount_micros"`
	AmountUSD    string `json:"amount_usd"`
	Outcome      string `json:"outcome"`  // granted, duplicate, rejected, error
	Credited     bool   `json:"credited"` // granted or duplicate: the wallet has the credit
	Detail       string `json:"detail"`
	CreatedAt    int64  `json:"created_at"`
}

type fundingTotals struct {
	Enabled        bool   `json:"enabled"`
	FundedMilli    int64  `json:"funded_milli"` // counted transfers since event_since, as the budget counts
	FundedOG       string `json:"funded_og"`
	CompletedMilli int64  `json:"completed_milli"`
	CompletedOG    string `json:"completed_og"`
	BudgetMilli    int64  `json:"budget_milli"` // event_budget_og; 0 when the faucet is off
	BudgetOG       string `json:"budget_og"`
	Transfers      int    `json:"transfers"` // every row, any status
	Wallets        int    `json:"wallets"`   // distinct wallets with a counted transfer since event_since
}

type rewardTotals struct {
	Enabled       bool   `json:"enabled"`
	GrantedMicros int64  `json:"granted_micros"`
	GrantedUSD    string `json:"granted_usd"`
	Granted       int    `json:"granted"` // credited claims
	Claims        int    `json:"claims"`  // every claim row, any outcome
	PerClaimUSD   string `json:"per_claim_usd"`
}

var txHashRe = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

func (s *Server) txURL(hash string) string {
	if !txHashRe.MatchString(hash) {
		return ""
	}
	base := s.cfg.Faucet.ExplorerTxURL
	if base == "" {
		base = "https://chainscan-galileo.0g.ai/tx/"
	}
	return base + hash
}

// dashFundings lists funding rows, newest first; limit <= 0 means all.
func (s *Server) dashFundings(limit int) ([]dashFunding, error) {
	q := `SELECT f.id, f.session_id, f.box, f.wallet, f.status, f.amount_milli, f.tx_hash, f.detail,
		f.created_at, f.updated_at, COALESCE(s.name,''), COALESCE(s.email,''), COALESCE(s.telegram,'')
		FROM fundings f LEFT JOIN sessions s ON s.id = f.session_id ORDER BY f.id DESC`
	if limit > 0 {
		q += ` LIMIT ` + strconv.Itoa(limit)
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dashFunding{}
	for rows.Next() {
		var f dashFunding
		if err := rows.Scan(&f.ID, &f.SessionID, &f.Box, &f.Wallet, &f.Status, &f.AmountMilli, &f.TxHash,
			&f.Detail, &f.CreatedAt, &f.UpdatedAt, &f.Attendee, &f.Email, &f.Telegram); err != nil {
			return nil, err
		}
		f.AmountOG = fmtOG(f.AmountMilli)
		f.Counted = fundingCounted(f.Status)
		f.TxURL = s.txURL(f.TxHash)
		out = append(out, f)
	}
	return out, rows.Err()
}

// dashRewards lists reward claims, newest first; limit <= 0 means all.
func (s *Server) dashRewards(limit int) ([]dashReward, error) {
	q := `SELECT c.session_id, COALESCE(s.box,''), c.wallet, c.amount_micros, c.outcome, c.detail, c.created_at,
		COALESCE(s.name,''), COALESCE(s.email,''), COALESCE(s.telegram,'')
		FROM credits c LEFT JOIN sessions s ON s.id = c.session_id ORDER BY c.created_at DESC, c.session_id`
	if limit > 0 {
		q += ` LIMIT ` + strconv.Itoa(limit)
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dashReward{}
	for rows.Next() {
		var c dashReward
		if err := rows.Scan(&c.SessionID, &c.Box, &c.Wallet, &c.AmountMicros, &c.Outcome, &c.Detail, &c.CreatedAt,
			&c.Attendee, &c.Email, &c.Telegram); err != nil {
			return nil, err
		}
		c.AmountUSD = fmtUSD(c.AmountMicros)
		c.Credited = credited(c.Outcome)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Server) fundingTotals() (fundingTotals, error) {
	fc := s.cfg.Faucet
	t := fundingTotals{Enabled: fc.enabled()}
	if t.Enabled {
		t.BudgetMilli = milli(fc.EventBudgetOG)
	}
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status IN `+countedStatuses+` AND created_at >= ? THEN amount_milli END),0),
		COALESCE(SUM(CASE WHEN status = 'completed' AND created_at >= ? THEN amount_milli END),0),
		COUNT(*),
		COUNT(DISTINCT CASE WHEN status IN `+countedStatuses+` AND created_at >= ? THEN wallet END)
		FROM fundings`, fc.since, fc.since, fc.since).Scan(&t.FundedMilli, &t.CompletedMilli, &t.Transfers, &t.Wallets)
	t.FundedOG, t.CompletedOG, t.BudgetOG = fmtOG(t.FundedMilli), fmtOG(t.CompletedMilli), fmtOG(t.BudgetMilli)
	return t, err
}

func (s *Server) rewardTotals() (rewardTotals, error) {
	t := rewardTotals{Enabled: s.cfg.Pay.enabled()}
	if t.Enabled {
		t.PerClaimUSD = s.cfg.Pay.amountUSD()
	}
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN outcome IN ('granted','duplicate') THEN amount_micros END),0),
		COUNT(CASE WHEN outcome IN ('granted','duplicate') THEN 1 END), COUNT(*) FROM credits`).
		Scan(&t.GrantedMicros, &t.Granted, &t.Claims)
	t.GrantedUSD = fmtUSD(t.GrantedMicros)
	return t, err
}

// ---------------------------------------------------------------- CSV exports

// csvCell defuses spreadsheet formulas in values that came from attendees.
func csvCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func csvTime(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

func writeCSV(w http.ResponseWriter, name string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	cw.Write(header)
	for _, r := range rows {
		for i := range r {
			r[i] = csvCell(r[i])
		}
		cw.Write(r)
	}
	cw.Flush()
}

func (s *Server) dashFundingsCSV(w http.ResponseWriter, r *http.Request) {
	list, err := s.dashFundings(0)
	if err != nil {
		s.serverError(w, err)
		return
	}
	rows := make([][]string, 0, len(list))
	for _, f := range list {
		rows = append(rows, []string{csvTime(f.CreatedAt), f.Box, f.SessionID, f.Attendee, f.Email, f.Telegram,
			f.Wallet, f.AmountOG, f.Status, boolStr(f.Counted), f.TxHash, f.TxURL, f.Detail})
	}
	writeCSV(w, "fundings.csv", []string{"created_at", "box", "session_id", "attendee", "email", "telegram",
		"wallet", "amount_og", "status", "counted", "tx_hash", "tx_url", "detail"}, rows)
}

func (s *Server) dashRewardsCSV(w http.ResponseWriter, r *http.Request) {
	list, err := s.dashRewards(0)
	if err != nil {
		s.serverError(w, err)
		return
	}
	rows := make([][]string, 0, len(list))
	for _, c := range list {
		rows = append(rows, []string{csvTime(c.CreatedAt), c.Box, c.SessionID, c.Attendee, c.Email, c.Telegram,
			c.Wallet, c.AmountUSD, c.Outcome, boolStr(c.Credited), c.Detail})
	}
	writeCSV(w, "rewards.csv", []string{"created_at", "box", "session_id", "attendee", "email", "telegram",
		"wallet", "amount_usd", "outcome", "credited", "detail"}, rows)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
