package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FaucetConfig lets an attendee's agent fund a wallet with testnet 0G during the session
// (POST /d/{token}/fund), through the 0G faucet's service-account API
// (github.com/0gfoundation/faucet, docs/swagger.yaml, tag "Bot Integration"). The API key
// stays on the hub; boxes only know their session's download token. Empty api_key turns the
// feature off; the endpoints then answer 404.
//
// Amounts are in 0G. Every cap counts transfers that are queued, executing, completed, or
// whose outcome is unknown (the faucet did not answer); failed and refused ones are free.
type FaucetConfig struct {
	APIURL string `json:"api_url"` // default https://faucet-api.udhaykumarbala.dev
	APIKey string `json:"api_key"` // fsk_...; main also reads FAUCET_API_KEY from the environment
	// PromoCode, when set, is an api_only code this key minted (POST /v1/promo-codes); every
	// transfer redeems it, so the faucet sends the code's amount instead of the fixed drip and
	// the code's per_wallet_limit and max_uses apply instead of its 24 h wallet limit.
	PromoCode string `json:"promo_code"`
	// AmountOG is what one transfer sends: the faucet's fixed drip (0.5) or the promo code's
	// amount. The faucet's own reply replaces it per transfer when it states the amount.
	AmountOG       float64 `json:"amount_og"`
	SessionMaxOG   float64 `json:"session_max_og"`    // per session, over all its wallets; default 1
	WalletMaxOG    float64 `json:"wallet_max_og"`     // per wallet, over all sessions; default 1
	BoxHourlyMaxOG float64 `json:"box_hourly_max_og"` // per box in any 60 minutes; default 2
	EventBudgetOG  float64 `json:"event_budget_og"`   // everything since event_since; default 50
	EventSince     string  `json:"event_since"`       // optional RFC 3339 start of the event budget
	IPPerMinute    int     `json:"ip_per_minute"`     // POST /fund per client address; default 30
	RefTag         string  `json:"ref_tag"`           // request_id stem: <ref_tag>:<session>:<wallet>; default hackbox
	ExplorerTxURL  string  `json:"explorer_tx_url"`   // default https://chainscan-galileo.0g.ai/tx/

	since int64 // EventSince in Unix seconds
}

func (f FaucetConfig) enabled() bool { return f.APIKey != "" }

var refTagRe = regexp.MustCompile(`^[a-zA-Z0-9-]{1,16}$`)

func (f *FaucetConfig) normalize() error {
	if !f.enabled() {
		return nil
	}
	f.APIKey = strings.TrimSpace(f.APIKey)
	if f.APIURL == "" {
		f.APIURL = "https://faucet-api.udhaykumarbala.dev"
	}
	f.APIURL = strings.TrimRight(f.APIURL, "/")
	if !strings.HasPrefix(f.APIURL, "https://") && !strings.HasPrefix(f.APIURL, "http://") {
		return fmt.Errorf("config: faucet.api_url must start with https://")
	}
	if strings.ContainsAny(f.APIKey, " \t\r\n") {
		return fmt.Errorf("config: faucet.api_key has white space in it")
	}
	f.PromoCode = strings.TrimSpace(f.PromoCode)
	def := func(v *float64, d float64) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&f.AmountOG, 0.5)
	def(&f.SessionMaxOG, 1)
	def(&f.WalletMaxOG, 1)
	def(&f.BoxHourlyMaxOG, 2)
	def(&f.EventBudgetOG, 50)
	if f.IPPerMinute <= 0 {
		f.IPPerMinute = 30
	}
	if f.RefTag == "" {
		f.RefTag = "hackbox"
	}
	if !refTagRe.MatchString(f.RefTag) {
		return fmt.Errorf("config: faucet.ref_tag: letters, digits and dashes only, 16 at most (request_id is 80 characters at most)")
	}
	if f.ExplorerTxURL == "" {
		f.ExplorerTxURL = "https://chainscan-galileo.0g.ai/tx/"
	}
	f.since = 0
	if f.EventSince != "" {
		t, err := time.Parse(time.RFC3339, f.EventSince)
		if err != nil {
			return fmt.Errorf("config: faucet.event_since: %w", err)
		}
		f.since = t.Unix()
	}
	for _, c := range []struct {
		name string
		v    float64
	}{{"session_max_og", f.SessionMaxOG}, {"wallet_max_og", f.WalletMaxOG}, {"box_hourly_max_og", f.BoxHourlyMaxOG}, {"event_budget_og", f.EventBudgetOG}} {
		if c.v < f.AmountOG {
			return fmt.Errorf("config: faucet.%s (%g) is below amount_og (%g): nothing could be sent", c.name, c.v, f.AmountOG)
		}
	}
	return nil
}

// Amounts are kept as integer milli-0G so the caps add up exactly.
func milli(og float64) int64 { return int64(math.Round(og * 1000)) }

func fmtOG(m int64) string {
	s := strconv.FormatInt(m/1000, 10)
	if frac := m % 1000; frac != 0 {
		s += strings.TrimRight(fmt.Sprintf(".%03d", frac), "0")
	}
	return s
}

// parseOG reads the faucet's decimal amount ("0.5", "1") as milli-0G.
func parseOG(s string) (int64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || f > 1e6 {
		return 0, false
	}
	return milli(f), true
}

// ---------------------------------------------------------------- the faucet API

type faucetTransferReq struct {
	RequestID string         `json:"request_id"`
	Wallet    string         `json:"wallet"`
	UserID    string         `json:"user_id"`
	PromoCode string         `json:"promo_code,omitempty"`
	Reason    string         `json:"reason"`
	Metadata  map[string]any `json:"metadata"`
}

type faucetTransfer struct {
	TransferID     string  `json:"transfer_id"`
	RequestID      string  `json:"request_id"`
	Status         string  `json:"status"` // queued, executing, completed, failed
	Amount         string  `json:"amount"`
	TxHash         *string `json:"tx_hash"`
	FailureReason  *string `json:"failure_reason"`
	Error          string  `json:"error"`
	AvailableAfter string  `json:"available_after"`
}

// faucetCall does one request against the faucet and decodes the JSON reply. A network
// failure comes back as err; any HTTP answer comes back as its code and body.
func (s *Server) faucetCall(ctx context.Context, method, path string, body any) (int, faucetTransfer, error) {
	var out faucetTransfer
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, out, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.Faucet.APIURL+path, rd)
	if err != nil {
		return 0, out, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Faucet.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.faucetHTTP.Do(req)
	if err != nil {
		return 0, out, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 120 {
			msg = msg[:120]
		}
		out.Error = msg
	}
	return resp.StatusCode, out, nil
}

// ---------------------------------------------------------------- fundings table

// funding is one row of the fundings table: one request_id sent to the faucet.
type funding struct {
	ID          int64
	SessionID   string
	Box         string
	Wallet      string
	RequestID   string
	TransferID  string
	Status      string // queued, executing, completed, failed, refused, unknown
	AmountMilli int64
	TxHash      string
	Detail      string
	CreatedAt   int64
	UpdatedAt   int64
}

// counted: the statuses that use up a cap (money sent, on its way, or maybe sent).
const countedStatuses = `('queued','executing','completed','unknown')`

func fundingCounted(st string) bool { return strings.Contains(countedStatuses, "'"+st+"'") }

func fundingFinal(st string) bool { return st == "completed" || st == "failed" || st == "refused" }

const fundingCols = `id, session_id, box, wallet, request_id, transfer_id, status, amount_milli, tx_hash, detail, created_at, updated_at`

func scanFunding(sc scanner) (*funding, error) {
	var f funding
	err := sc.Scan(&f.ID, &f.SessionID, &f.Box, &f.Wallet, &f.RequestID, &f.TransferID, &f.Status,
		&f.AmountMilli, &f.TxHash, &f.Detail, &f.CreatedAt, &f.UpdatedAt)
	return &f, err
}

func (s *Server) fundingsOf(sessionID string) ([]*funding, error) {
	rows, err := s.db.Query(`SELECT `+fundingCols+` FROM fundings WHERE session_id = ? ORDER BY id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*funding
	for rows.Next() {
		f, err := scanFunding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Server) saveFunding(f *funding) error {
	f.UpdatedAt = s.now().Unix()
	if f.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO fundings (session_id, box, wallet, request_id, transfer_id, status,
			amount_milli, tx_hash, detail, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			f.SessionID, f.Box, f.Wallet, f.RequestID, f.TransferID, f.Status, f.AmountMilli, f.TxHash,
			f.Detail, f.CreatedAt, f.UpdatedAt)
		if err != nil {
			return err
		}
		f.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.db.Exec(`UPDATE fundings SET transfer_id = ?, status = ?, amount_milli = ?, tx_hash = ?, detail = ?,
		updated_at = ? WHERE id = ?`, f.TransferID, f.Status, f.AmountMilli, f.TxHash, f.Detail, f.UpdatedAt, f.ID)
	return err
}

// fundedSum adds up the counted rows matching where, leaving out row except.
func (s *Server) fundedSum(except int64, where string, args ...any) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(amount_milli),0) FROM fundings WHERE status IN `+countedStatuses+
		` AND id != ? AND `+where, append([]any{except}, args...)...).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------- replies

type fundReply struct {
	Outcome     string `json:"outcome"`
	Wallet      string `json:"wallet,omitempty"`
	Amount      string `json:"amount,omitempty"` // 0G, e.g. "0.5"
	Status      string `json:"status,omitempty"` // the faucet transfer: queued, executing, completed, failed
	TxHash      string `json:"tx_hash,omitempty"`
	ExplorerURL string `json:"explorer_url,omitempty"`
	Remaining   string `json:"session_remaining,omitempty"` // 0G this session can still send
	Message     string `json:"message"`
}

type fundList struct {
	Active    bool        `json:"active"`
	Amount    string      `json:"amount"`            // 0G per transfer
	SessionOG string      `json:"session_max"`       // 0G per session
	Remaining string      `json:"session_remaining"` // 0G this session can still send
	Transfers []fundReply `json:"transfers"`         // newest first
}

func (s *Server) fundReplyOf(f *funding) fundReply {
	r := fundReply{Outcome: f.Status, Wallet: f.Wallet, Amount: fmtOG(f.AmountMilli), TxHash: f.TxHash}
	if f.Status != "refused" && f.Status != "unknown" {
		r.Status = f.Status
	}
	if f.TxHash != "" {
		r.ExplorerURL = s.cfg.Faucet.ExplorerTxURL + f.TxHash
	}
	amt := r.Amount + " 0G"
	switch f.Status {
	case "queued":
		r.Message = "Sending " + amt + " to " + f.Wallet + ". It usually arrives within a minute; GET the same URL to follow it (0g-fund does that for you)."
	case "executing":
		r.Message = "Sending " + amt + " to " + f.Wallet + ": transaction " + f.TxHash + " is waiting for confirmation."
	case "completed":
		r.Message = amt + " (testnet) arrived on " + f.Wallet + ". Transaction " + f.TxHash + "."
		if f.TxHash == "" {
			r.Message = amt + " (testnet) was sent to " + f.Wallet + "."
		}
	case "failed":
		r.Message = "The faucet could not send to " + f.Wallet + " (" + f.Detail + "). Run the same command again to retry."
	case "unknown":
		r.Message = "The faucet did not answer. Run the same command again in a minute; repeating it never sends twice."
	default:
		r.Message = f.Detail
	}
	return r
}

// ---------------------------------------------------------------- the endpoints

// faucetSession looks up the session for /d/{token}/fund and answers the error itself.
func (s *Server) faucetSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	if !s.cfg.Faucet.enabled() {
		jsonError(w, http.StatusNotFound, "testnet 0G funding is not enabled on this hub")
		return nil, false
	}
	ss, err := s.sessionBy("token", r.PathValue("token"))
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, fundReply{Outcome: "error", Message: "Unknown session link. Read the URL again from /run/hackbox/hub-url."})
		return nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	return ss, true
}

// fundActiveWindow: the box must have reported this session as active this recently.
const fundActiveWindow = 30

// sessionActive says whether the box is in this session right now, by its heartbeats.
func (s *Server) sessionActive(ss *Session) (bool, error) {
	if ss.EndedAt != 0 {
		return false, nil
	}
	var state, sid string
	var seen int64
	err := s.db.QueryRow(`SELECT state, session_id, last_seen FROM boxes WHERE name = ?`, ss.Box).Scan(&state, &sid, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == "active" && sid == ss.ID && s.now().Unix()-seen <= fundActiveWindow, nil
}

// refreshFunding asks the faucet for a transfer that is still on its way, at most every 2 s.
func (s *Server) refreshFunding(ctx context.Context, f *funding) {
	if fundingFinal(f.Status) || f.TransferID == "" || s.now().Unix()-f.UpdatedAt < 2 {
		return
	}
	code, out, err := s.faucetCall(ctx, "GET", "/v1/transfers/"+f.TransferID, nil)
	if err != nil || code != 200 {
		if err == nil {
			err = fmt.Errorf("HTTP %d %s", code, out.Error)
		}
		s.log.Printf("faucet: status of %s: %v", f.TransferID, err)
		return
	}
	s.applyTransfer(f, out)
	if err := s.saveFunding(f); err != nil {
		s.log.Printf("error: save funding: %v", err)
	}
}

// applyTransfer copies the faucet's view of a transfer into the row.
func (s *Server) applyTransfer(f *funding, t faucetTransfer) {
	if t.TransferID != "" {
		f.TransferID = t.TransferID
	}
	switch t.Status {
	case "queued", "executing", "completed", "failed":
		f.Status = t.Status
	}
	if t.TxHash != nil {
		f.TxHash = *t.TxHash
	}
	if t.FailureReason != nil {
		f.Detail = cleanText(*t.FailureReason, 200)
	}
	if m, ok := parseOG(t.Amount); ok {
		f.AmountMilli = m
	}
}

// pageFundStatus is GET /d/{token}/fund: the session's transfers (refreshed from the faucet
// while they are on their way) and what the session can still send. Works after the session
// too, as long as the link is valid.
func (s *Server) pageFundStatus(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.faucetSession(w, r)
	if !ok {
		return
	}
	if !s.fundReadLimiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, fundReply{Outcome: "error", Message: "Too many status checks. Wait a few seconds between checks."})
		return
	}
	if s.sessionState(ss) == "expired" {
		writeJSON(w, http.StatusGone, fundReply{Outcome: "error", Message: "This session link has expired."})
		return
	}
	wallet := ""
	if q := r.URL.Query().Get("wallet"); q != "" {
		var err error
		if wallet, err = normalizeWallet(q); err != nil {
			writeJSON(w, http.StatusBadRequest, fundReply{Outcome: "error", Message: err.Error()})
			return
		}
	}
	s.fundMu.Lock()
	defer s.fundMu.Unlock()
	list, err := s.fundingsOf(ss.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	active, err := s.sessionActive(ss)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := fundList{Active: active, Amount: fmtOG(milli(s.cfg.Faucet.AmountOG)), SessionOG: fmtOG(milli(s.cfg.Faucet.SessionMaxOG)),
		Transfers: []fundReply{}}
	var used int64
	for _, f := range list {
		if wallet == "" || f.Wallet == wallet {
			s.refreshFunding(ctx, f)
			out.Transfers = append(out.Transfers, s.fundReplyOf(f))
		}
		if fundingCounted(f.Status) {
			used += f.AmountMilli
		}
	}
	out.Remaining = fmtOG(max(0, milli(s.cfg.Faucet.SessionMaxOG)-used))
	writeJSON(w, http.StatusOK, out)
}

// pageFund is POST /d/{token}/fund {"wallet": "0x..."}: sends testnet 0G from the faucet to
// the wallet, while the session is active, within the caps. Repeating the call for the same
// session and wallet never sends twice: it returns that transfer's current state.
func (s *Server) pageFund(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.faucetSession(w, r)
	if !ok {
		return
	}
	ip := clientIP(r)
	if !s.fundLimiter.allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, fundReply{Outcome: "error", Message: "Too many funding requests from this network. Wait a minute and try again."})
		return
	}
	var in struct {
		Wallet string `json:"wallet"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	wallet, err := normalizeWallet(in.Wallet)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, fundReply{Outcome: "error", Message: err.Error() + ". Send {\"wallet\": \"0x...\"}."})
		return
	}
	fc := s.cfg.Faucet

	// One request at a time: the caps are checked and then spent.
	s.fundMu.Lock()
	defer s.fundMu.Unlock()

	list, err := s.fundingsOf(ss.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	var row *funding // the newest row of this session and wallet
	attempts := 0
	for _, f := range list {
		if f.Wallet == wallet {
			attempts++
			if row == nil {
				row = f
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	remaining := func() string {
		used, err := s.fundedSum(0, "session_id = ?", ss.ID)
		if err != nil {
			return ""
		}
		return fmtOG(max(0, milli(fc.SessionMaxOG)-used))
	}

	// Already on its way or done: report it, never send again.
	if row != nil && (row.Status == "queued" || row.Status == "executing" || row.Status == "completed") {
		s.refreshFunding(ctx, row)
		rep := s.fundReplyOf(row)
		rep.Remaining = remaining()
		if row.Status == "completed" {
			rep.Outcome = "already"
			rep.Message = "This session already funded " + wallet + ": " + rep.Message
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}

	active, err := s.sessionActive(ss)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !active {
		writeJSON(w, http.StatusForbidden, fundReply{Outcome: "inactive", Wallet: wallet,
			Message: "Funding works only while this hack-box session is running. The hub does not see this session as active: it has ended, or the box has not checked in for 30 seconds."})
		return
	}

	// Reuse the request_id after a refusal or an unknown outcome (the faucet either has no
	// transfer for it or returns the one it has); a failed transfer used its id up.
	var f *funding
	switch {
	case row != nil && (row.Status == "refused" || row.Status == "unknown"):
		f = row
	default:
		attempts++
		rid := fc.RefTag + ":" + ss.ID + ":" + wallet
		if attempts > 1 {
			rid += ":" + strconv.Itoa(attempts)
		}
		f = &funding{SessionID: ss.ID, Box: ss.Box, Wallet: wallet, RequestID: rid, CreatedAt: s.now().Unix()}
	}
	f.AmountMilli = milli(fc.AmountOG)

	// The caps, cheapest message first.
	now := s.now().Unix()
	caps := []struct {
		max   float64
		where string
		args  []any
		msg   string
	}{
		{fc.SessionMaxOG, "session_id = ?", []any{ss.ID},
			"This session has used its testnet 0G allowance (%s 0G per session). Use the wallet that was already funded."},
		{fc.WalletMaxOG, "wallet = ?", []any{wallet},
			"This wallet has already received %s 0G from hack-box sessions, the most one wallet can get."},
		{fc.BoxHourlyMaxOG, "box = ? AND created_at > ?", []any{ss.Box, now - 3600},
			"This hack-box has sent its hourly allowance of %s 0G. Try again later or ask a staff member."},
		{fc.EventBudgetOG, "created_at >= ?", []any{fc.since},
			"The event's testnet 0G budget (%s 0G) is used up. Please ask a staff member."},
	}
	for _, c := range caps {
		used, err := s.fundedSum(f.ID, c.where, c.args...)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if used+f.AmountMilli > milli(c.max) {
			writeJSON(w, http.StatusConflict, fundReply{Outcome: "limit", Wallet: wallet, Remaining: remaining(),
				Message: fmt.Sprintf(c.msg, fmtOG(milli(c.max)))})
			return
		}
	}

	req := faucetTransferReq{RequestID: f.RequestID, Wallet: wallet, UserID: f.RequestID, PromoCode: fc.PromoCode,
		Reason: "hack-box session funding", Metadata: map[string]any{"source": "0g-hack-box", "box": ss.Box, "session": ss.ID}}
	code, out, err := s.faucetCall(ctx, "POST", "/v1/transfers", req)
	httpCode := http.StatusAccepted
	switch {
	case err != nil:
		f.Status, f.Detail = "unknown", cleanText(err.Error(), 200)
		httpCode = http.StatusBadGateway
	case code == 200 || code == 202:
		f.Status, f.Detail = "unknown", ""
		s.applyTransfer(f, out)
		if code == 200 {
			httpCode = http.StatusOK
		}
		if f.Status == "failed" {
			httpCode = http.StatusBadGateway
		}
	case code == 429:
		f.Status = "refused"
		f.Detail = "The faucet sent this wallet testnet 0G in the last 24 hours (that limit is shared with the public faucet)"
		if t, perr := time.Parse(time.RFC3339, out.AvailableAfter); perr == nil {
			f.Detail += "; it can get more after " + t.UTC().Format("15:04 UTC on 2 Jan")
		}
		f.Detail += ". Fund a new wallet instead, or use this one as it is."
		httpCode = http.StatusTooManyRequests
	case code == 409:
		f.Status, f.Detail = "refused", "The faucet refused: "+cleanText(out.Error, 120)+". Use another wallet, or ask a staff member."
		httpCode = http.StatusConflict
	case code == 400:
		f.Status, f.Detail = "refused", "The faucet refused the request: "+cleanText(out.Error, 120)+"."
		httpCode = http.StatusBadRequest
	case code == 401 || code == 403 || code == 404:
		f.Status, f.Detail = "refused", "Funding is not available right now (hub configuration). Please ask a staff member."
		httpCode = http.StatusBadGateway
	default:
		f.Status, f.Detail = "unknown", fmt.Sprintf("HTTP %d %s", code, cleanText(out.Error, 120))
		httpCode = http.StatusBadGateway
	}
	if f.Status == "unknown" && f.TransferID != "" {
		f.Status = "queued" // accepted without a status we know: treat as on its way
	}
	if f.Status != "queued" && f.Status != "executing" && f.Status != "completed" {
		s.log.Printf("faucet: session %s wallet %s: %s %s", ss.ID, wallet, f.Status, f.Detail)
	}
	if serr := s.saveFunding(f); serr != nil {
		s.log.Printf("error: save funding: %v", serr)
	}
	rep := s.fundReplyOf(f)
	rep.Remaining = remaining()
	writeJSON(w, httpCode, rep)
}
