package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// PayConfig is the 0G Pay partner regrant: at the end of a session the attendee connects a
// wallet on the download page and the hub grants it compute credit from the partner's lot
// (https://pay.0g.ai/docs/partner-regrant). The values come from the partner kit. Empty
// api_url, source_lot_id or signer_key turns the feature off; the page then shows no credit
// section and POST /d/{token}/credit answers 404.
type PayConfig struct {
	APIURL       string `json:"api_url"`        // https://<pay-api-host>, no path
	SourceLotID  string `json:"source_lot_id"`  // 32 hex characters, the partner's lot
	SignerKey    string `json:"signer_key"`     // hex private key of the seated signer wallet
	AmountMicros int64  `json:"amount_micros"`  // per grant; default 10000000 ($10.00)
	MaxPerWallet int    `json:"max_per_wallet"` // grants one wallet may receive in all; default 2
	RefTag       string `json:"ref_tag"`        // middle part of external_ref; default hackbox
	ExpiresAt    string `json:"expires_at"`     // optional ISO 8601; empty inherits the lot's expiry
	SpendURL     string `json:"spend_url"`      // where to spend it; default https://pc.0g.ai
	ExpiryLabel  string `json:"expiry_label"`   // shown after the claim, e.g. "13 October 2026"; empty hides it
}

func (p PayConfig) enabled() bool {
	return p.APIURL != "" || p.SourceLotID != "" || p.SignerKey != ""
}

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (p *PayConfig) normalize() error {
	if !p.enabled() {
		return nil
	}
	p.APIURL = strings.TrimRight(p.APIURL, "/")
	p.APIURL = strings.TrimRight(strings.TrimSuffix(p.APIURL, regrantPath), "/") // the kit may give the full endpoint
	p.SourceLotID = strings.ToLower(strings.TrimSpace(p.SourceLotID))
	p.SignerKey = strings.TrimSpace(p.SignerKey)
	if !strings.HasPrefix(p.APIURL, "http://") && !strings.HasPrefix(p.APIURL, "https://") {
		return fmt.Errorf("config: pay.api_url must start with https://")
	}
	if !hex32.MatchString(p.SourceLotID) {
		return fmt.Errorf("config: pay.source_lot_id must be 32 hex characters")
	}
	if _, err := newPaySigner(p.SignerKey); err != nil {
		return fmt.Errorf("config: pay.signer_key: %w", err)
	}
	if p.AmountMicros <= 0 {
		p.AmountMicros = 10_000_000
	}
	if p.MaxPerWallet <= 0 {
		p.MaxPerWallet = 2
	}
	if p.RefTag == "" {
		p.RefTag = "hackbox"
	}
	if strings.ContainsAny(p.RefTag, ": \t\n") || len(p.RefTag) > 40 {
		return fmt.Errorf("config: pay.ref_tag: letters, digits and dashes only, 40 at most")
	}
	if p.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, p.ExpiresAt); err != nil {
			return fmt.Errorf("config: pay.expires_at: %w", err)
		}
	}
	if p.SpendURL == "" {
		p.SpendURL = "https://pc.0g.ai"
	}
	if !strings.HasPrefix(p.SpendURL, "https://") {
		return fmt.Errorf("config: pay.spend_url must start with https://")
	}
	p.ExpiryLabel = cleanText(p.ExpiryLabel, 40)
	return nil
}

// amountUSD renders amount_micros as "$10" or "$2.50".
func (p PayConfig) amountUSD() string {
	whole, frac := p.AmountMicros/1_000_000, p.AmountMicros%1_000_000
	switch {
	case frac == 0:
		return fmt.Sprintf("$%d", whole)
	case frac%10_000 == 0: // whole cents: "$0.50"
		return fmt.Sprintf("$%d.%02d", whole, frac/10_000)
	}
	return strings.TrimRight(fmt.Sprintf("$%d.%06d", whole, frac), "0")
}

// ---------------------------------------------------------------- signing

// paySigner holds the partner signer wallet.
type paySigner struct {
	key     *secp256k1.PrivateKey
	address string // 0x + 40 lowercase hex
}

func newPaySigner(hexKey string) (*paySigner, error) {
	h := strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		return nil, errors.New("want 32 bytes of hex")
	}
	key := secp256k1.PrivKeyFromBytes(b)
	if key.Key.IsZero() {
		return nil, errors.New("key is zero")
	}
	return &paySigner{key: key, address: ethAddress(key.PubKey())}, nil
}

func keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// ethAddress is the lowercase 0x address of a public key.
func ethAddress(pub *secp256k1.PublicKey) string {
	raw := pub.SerializeUncompressed()[1:] // drop the 0x04 prefix
	return "0x" + hex.EncodeToString(keccak256(raw)[12:])
}

// sign is EIP-191 personal_sign: 0x + r||s||v (v = 27 or 28).
func (s *paySigner) sign(msg string) string {
	prefixed := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)) + msg)
	digest := keccak256(prefixed)
	compact := ecdsa.SignCompact(s.key, digest, false) // [v, r, s]
	sig := make([]byte, 65)
	copy(sig, compact[1:])
	sig[64] = compact[0]
	return "0x" + hex.EncodeToString(sig)
}

// recoverPersonalSign returns the lowercase address that signed msg with EIP-191
// personal_sign; sig is 0x + r||s||v. Tests and the fake Pay API use it.
func recoverPersonalSign(msg, sig string) (string, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(sig, "0x"))
	if err != nil || len(b) != 65 {
		return "", errors.New("signature: want 65 bytes of hex")
	}
	v := b[64]
	if v < 27 {
		v += 27
	}
	compact := append([]byte{v}, b[:64]...)
	digest := keccak256([]byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)) + msg))
	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return "", err
	}
	return ethAddress(pub), nil
}

// ---------------------------------------------------------------- wallet addresses

var addrRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// normalizeWallet checks an EVM address and returns it lowercase. Mixed-case input must
// carry a valid EIP-55 checksum, which catches typing mistakes in pasted addresses.
func normalizeWallet(a string) (string, error) {
	a = strings.TrimSpace(a)
	if strings.HasPrefix(a, "0X") {
		a = "0x" + a[2:]
	}
	if !addrRe.MatchString(a) {
		return "", errors.New("that is not an EVM address (0x and 40 hex characters)")
	}
	body := a[2:]
	lower := strings.ToLower(body)
	if body == lower || body == strings.ToUpper(body) {
		return "0x" + lower, nil
	}
	sum := hex.EncodeToString(keccak256([]byte(lower)))
	for i := 0; i < 40; i++ {
		c := body[i]
		if c >= '0' && c <= '9' {
			continue
		}
		upper := sum[i] >= '8'
		if (c >= 'A' && c <= 'F') != upper {
			return "", errors.New("the address has a typo (checksum mismatch); copy it again")
		}
	}
	return "0x" + lower, nil
}

// ---------------------------------------------------------------- the Pay API

type regrantRequest struct {
	SourceLotID string        `json:"source_lot_id"`
	Items       []regrantItem `json:"items"`
}

type regrantItem struct {
	ExternalRef  string     `json:"external_ref"`
	AccountRef   accountRef `json:"account_ref"`
	AmountMicros int64      `json:"amount_micros"`
	ExpiresAt    string     `json:"expires_at,omitempty"`
}

type accountRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type regrantResponse struct {
	GrantedCount   int    `json:"granted_count"`
	DuplicateCount int    `json:"duplicate_count"`
	RejectedCount  int    `json:"rejected_count"`
	Error          string `json:"error"`
	Reason         string `json:"reason"`
	Items          []struct {
		ExternalRef  string `json:"external_ref"`
		Outcome      string `json:"outcome"`
		Reason       string `json:"reason"`
		AmountMicros int64  `json:"amount_micros"`
		ExpiresAt    string `json:"expires_at"`
	} `json:"items"`
}

const regrantPath = "/v1/regrants"

// regrant posts one grant and returns the row outcome (granted, duplicate, rejected) and
// the rejection reason. Network and whole-batch failures come back as an error.
func (s *Server) regrant(ctx context.Context, externalRef, wallet string) (outcome, reason string, err error) {
	req := regrantRequest{SourceLotID: s.cfg.Pay.SourceLotID, Items: []regrantItem{{
		ExternalRef:  externalRef,
		AccountRef:   accountRef{Kind: "evm_address", ID: wallet},
		AmountMicros: s.cfg.Pay.AmountMicros,
		ExpiresAt:    s.cfg.Pay.ExpiresAt,
	}}}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	// The signature covers the exact wire bytes: hash body as sent.
	sum := sha256.Sum256(body)
	ts := strconv.FormatInt(s.now().Unix(), 10)
	msg := "pay-v2|POST|" + regrantPath + "|" + ts + "|" + hex.EncodeToString(sum[:])
	hr, err := http.NewRequestWithContext(ctx, "POST", s.cfg.Pay.APIURL+regrantPath, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("X-Pay-Timestamp", ts)
	hr.Header.Set("X-Pay-Signature", s.pay.sign(msg))
	resp, err := s.payHTTP.Do(hr)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out regrantResponse
	if jerr := json.Unmarshal(raw, &out); jerr != nil && resp.StatusCode == 200 {
		return "", "", fmt.Errorf("pay: bad response: %v", jerr)
	}
	if resp.StatusCode != 200 {
		code := out.Error
		if code == "" {
			code = out.Reason
		}
		if code == "" {
			code = strings.TrimSpace(string(raw))
			if len(code) > 120 {
				code = code[:120]
			}
		}
		return "", "", fmt.Errorf("pay: HTTP %d %s", resp.StatusCode, code)
	}
	for _, it := range out.Items {
		if it.ExternalRef == externalRef {
			return it.Outcome, it.Reason, nil
		}
	}
	if len(out.Items) == 1 {
		return out.Items[0].Outcome, out.Items[0].Reason, nil
	}
	return "", "", errors.New("pay: our row is missing from the response")
}

// ---------------------------------------------------------------- credits table

// credit is one row of the credits table: a session's one grant attempt.
type credit struct {
	SessionID    string
	Wallet       string
	ExternalRef  string
	AmountMicros int64
	Outcome      string // granted, duplicate, rejected, error
	Detail       string
	CreatedAt    int64
}

func (s *Server) creditBySession(id string) (*credit, error) {
	var c credit
	err := s.db.QueryRow(`SELECT session_id, wallet, external_ref, amount_micros, outcome, detail, created_at
		FROM credits WHERE session_id = ?`, id).Scan(&c.SessionID, &c.Wallet, &c.ExternalRef, &c.AmountMicros,
		&c.Outcome, &c.Detail, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return &c, err
}

// credited says whether a stored outcome means the wallet got the money.
func credited(outcome string) bool { return outcome == "granted" || outcome == "duplicate" }

// walletGrants counts the grants one wallet received in all.
func (s *Server) walletGrants(wallet string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM credits WHERE wallet = ? AND outcome IN ('granted','duplicate')`,
		wallet).Scan(&n)
	return n, err
}

func (s *Server) saveCredit(c credit) error {
	_, err := s.db.Exec(`INSERT INTO credits (session_id, wallet, external_ref, amount_micros, outcome, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET wallet = excluded.wallet, external_ref = excluded.external_ref,
		amount_micros = excluded.amount_micros, outcome = excluded.outcome, detail = excluded.detail,
		created_at = excluded.created_at`,
		c.SessionID, c.Wallet, c.ExternalRef, c.AmountMicros, c.Outcome, c.Detail, c.CreatedAt)
	return err
}

// ---------------------------------------------------------------- the endpoint

// payState is what the download page knows about the session's credit.
type payState struct {
	Enabled  bool
	Done     bool   // the session's grant went through
	Wallet   string // the credited wallet (short form for display)
	Amount   string // "$10"
	Max      int
	SpendURL string
	SpendAt  string // host of SpendURL, for the button text
	Expiry   string // "13 October 2026" or ""
}

func shortWallet(w string) string {
	if len(w) < 12 {
		return w
	}
	return w[:6] + "…" + w[len(w)-4:]
}

func (s *Server) payStateFor(ss *Session) payState {
	st := payState{Enabled: s.cfg.Pay.enabled(), Amount: s.cfg.Pay.amountUSD(), Max: s.cfg.Pay.MaxPerWallet,
		SpendURL: s.cfg.Pay.SpendURL, Expiry: s.cfg.Pay.ExpiryLabel}
	if !st.Enabled {
		return st
	}
	st.SpendAt = strings.TrimPrefix(strings.TrimRight(strings.TrimPrefix(st.SpendURL, "https://"), "/"), "www.")
	if c, err := s.creditBySession(ss.ID); err == nil && credited(c.Outcome) {
		st.Done = true
		st.Wallet = shortWallet(c.Wallet)
	}
	return st
}

type creditReply struct {
	Outcome string `json:"outcome"` // granted, already, limit, rejected, error
	Wallet  string `json:"wallet,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Message string `json:"message"`
}

// pageCredit is POST /d/{token}/credit {"wallet": "0x..."}: grants the session's one credit
// to the wallet. One grant per session; max_per_wallet grants per wallet in all.
func (s *Server) pageCredit(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Pay.enabled() {
		jsonError(w, http.StatusNotFound, "compute credits are not enabled on this hub")
		return
	}
	if !s.payLimiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, creditReply{Outcome: "error", Message: "Too many tries. Wait a minute and try again."})
		return
	}
	ss, err := s.sessionBy("token", r.PathValue("token"))
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, creditReply{Outcome: "error", Message: "We could not find that session."})
		return
	}
	if err != nil {
		s.log.Printf("error: %v", err)
		jsonError(w, http.StatusInternalServerError, "internal error")
		return
	}
	switch s.sessionState(ss) {
	case "expired":
		writeJSON(w, http.StatusGone, creditReply{Outcome: "error", Message: "This link has expired."})
		return
	case "preparing":
		writeJSON(w, http.StatusConflict, creditReply{Outcome: "error", Message: "The session is still being saved. Try again in a moment."})
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
		writeJSON(w, http.StatusBadRequest, creditReply{Outcome: "error", Message: err.Error()})
		return
	}
	amount := s.cfg.Pay.amountUSD()

	// One claim at a time: the per-wallet cap is checked and then spent.
	s.payMu.Lock()
	defer s.payMu.Unlock()

	if c, err := s.creditBySession(ss.ID); err == nil && credited(c.Outcome) {
		msg := fmt.Sprintf("This session's %s credit already went to %s.", amount, shortWallet(c.Wallet))
		writeJSON(w, http.StatusOK, creditReply{Outcome: "already", Wallet: c.Wallet, Amount: amount, Message: msg})
		return
	} else if err != nil && !errors.Is(err, errNotFound) {
		s.log.Printf("error: %v", err)
		jsonError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, err := s.walletGrants(wallet)
	if err != nil {
		s.log.Printf("error: %v", err)
		jsonError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n >= s.cfg.Pay.MaxPerWallet {
		msg := fmt.Sprintf("%s has already received %d credits, the most one wallet can get. Use another wallet.", shortWallet(wallet), n)
		writeJSON(w, http.StatusConflict, creditReply{Outcome: "limit", Wallet: wallet, Amount: amount, Message: msg})
		return
	}

	ref := s.pay.address + ":" + s.cfg.Pay.RefTag + ":" + ss.ID
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	outcome, reason, err := s.regrant(ctx, ref, wallet)
	row := credit{SessionID: ss.ID, Wallet: wallet, ExternalRef: ref, AmountMicros: s.cfg.Pay.AmountMicros,
		CreatedAt: s.now().Unix()}
	switch {
	case err != nil:
		row.Outcome, row.Detail = "error", err.Error()
		s.log.Printf("pay: session %s wallet %s: %v", ss.ID, wallet, err)
	case credited(outcome):
		row.Outcome = outcome
	default:
		row.Outcome, row.Detail = "rejected", reason
		s.log.Printf("pay: session %s wallet %s rejected: %s", ss.ID, wallet, reason)
	}
	if serr := s.saveCredit(row); serr != nil {
		s.log.Printf("error: save credit: %v", serr)
	}
	switch row.Outcome {
	case "granted", "duplicate":
		msg := fmt.Sprintf("%s of 0G Compute credit is on %s.", amount, shortWallet(wallet))
		writeJSON(w, http.StatusOK, creditReply{Outcome: "granted", Wallet: wallet, Amount: amount, Message: msg})
	case "rejected":
		msg := "The credit could not be granted (" + reason + "). Please ask a staff member."
		if reason == "lot_unusable" {
			msg = "The credit pool is empty right now. Please ask a staff member."
		}
		writeJSON(w, http.StatusBadGateway, creditReply{Outcome: "rejected", Wallet: wallet, Message: msg})
	default:
		writeJSON(w, http.StatusBadGateway, creditReply{Outcome: "error", Wallet: wallet,
			Message: "The credit service did not answer. Try again in a minute, or ask a staff member."})
	}
}
