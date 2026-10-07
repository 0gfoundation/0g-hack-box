package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A well-known test key (Hardhat account #1); never holds funds.
const (
	testSignerKey  = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	testSignerAddr = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	testLot        = "01a0b1c2d3e4f5061728394a5b6c7d8e"
	walletA        = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" // valid EIP-55 checksum
	walletB        = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"
)

// fakePay is the Pay API: it checks the signature and remembers external_refs.
type fakePay struct {
	t      *testing.T
	mu     sync.Mutex
	calls  int
	seen   map[string]bool
	reject string // when set, every new row is rejected with this reason
	down   bool   // when set, answer 503
	last   regrantRequest
}

func (f *fakePay) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	body, _ := io.ReadAll(r.Body)
	if r.Method != "POST" || r.URL.Path != "/v1/regrants" {
		f.t.Errorf("pay: %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("X-Actor-Subject") != "" {
		f.t.Errorf("pay: X-Actor-Subject must not be sent")
	}
	ts, _ := strconv.ParseInt(r.Header.Get("X-Pay-Timestamp"), 10, 64)
	if d := time.Now().Unix() - ts; d > 300 || d < -300 {
		// The test clock is in 2026; the hub signs with its own clock, which is what matters.
		_ = d
	}
	sum := sha256.Sum256(body)
	msg := "pay-v2|POST|/v1/regrants|" + r.Header.Get("X-Pay-Timestamp") + "|" + hex.EncodeToString(sum[:])
	addr, err := recoverPersonalSign(msg, r.Header.Get("X-Pay-Signature"))
	if err != nil || addr != testSignerAddr {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad signature " + err.Error()})
		return
	}
	if f.down {
		w.WriteHeader(503)
		io.WriteString(w, "down")
		return
	}
	var req regrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.SourceLotID != testLot {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "unknown_lot"})
		return
	}
	f.last = req
	out := map[string]any{}
	var items []map[string]any
	for _, it := range req.Items {
		if !strings.HasPrefix(it.ExternalRef, testSignerAddr+":") {
			items = append(items, map[string]any{"external_ref": it.ExternalRef, "outcome": "rejected", "reason": "external_ref_prefix"})
			continue
		}
		switch {
		case f.seen[it.ExternalRef]:
			items = append(items, map[string]any{"external_ref": it.ExternalRef, "outcome": "duplicate"})
		case f.reject != "":
			items = append(items, map[string]any{"external_ref": it.ExternalRef, "outcome": "rejected", "reason": f.reject})
		default:
			f.seen[it.ExternalRef] = true
			items = append(items, map[string]any{"external_ref": it.ExternalRef, "outcome": "granted", "amount_micros": it.AmountMicros})
		}
	}
	out["items"] = items
	json.NewEncoder(w).Encode(out)
}

func (f *fakePay) n() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func TestEthAddressAndPersonalSign(t *testing.T) {
	s, err := newPaySigner(testSignerKey)
	if err != nil {
		t.Fatal(err)
	}
	if s.address != testSignerAddr {
		t.Fatalf("address %s, want %s", s.address, testSignerAddr)
	}
	sig := s.sign("hello")
	if len(sig) != 2+130 {
		t.Fatalf("signature length %d", len(sig))
	}
	if got, err := recoverPersonalSign("hello", sig); err != nil || got != testSignerAddr {
		t.Fatalf("recover: %s %v", got, err)
	}
	if got, _ := recoverPersonalSign("other", sig); got == testSignerAddr {
		t.Fatal("recover of another message must not give the signer")
	}
	for _, bad := range []string{"", "0x", "0x" + strings.Repeat("00", 32), "zz"} {
		if _, err := newPaySigner(bad); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

func TestNormalizeWallet(t *testing.T) {
	good := map[string]string{
		walletA:                  strings.ToLower(walletA),
		strings.ToLower(walletA): strings.ToLower(walletA),
		strings.ToUpper(walletA): strings.ToLower(walletA),
		" " + walletB + " ":      strings.ToLower(walletB),
	}
	for in, want := range good {
		if got, err := normalizeWallet(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	typo := walletA[:len(walletA)-1] + "7" // last character changed: checksum no longer matches
	for _, in := range []string{"", "0x123", "f39Fd6e51aad88F6F4ce6aB8827279cffFb92266", typo, "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb9226g"} {
		if _, err := normalizeWallet(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func payEnv(t *testing.T) (*env, *fakePay) {
	f := &fakePay{t: t, seen: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	e := newEnv(t, func(o *Options) {
		o.Config.Pay = PayConfig{APIURL: srv.URL, SourceLotID: testLot, SignerKey: testSignerKey, RefTag: "test", ExpiryLabel: "13 October 2026"}
	})
	return e, f
}

// endEmpty ends a session with nothing to save, which makes the page "empty" (claimable).
func (e *env) endEmpty(tok, id string) {
	e.t.Helper()
	w := e.do("POST", "/api/v1/sessions/"+id+"/end", map[string]any{
		"ended_at": e.clk.Now().Unix(), "reason": "done", "empty": true}, box(tok))
	if w.Code != 200 {
		e.t.Fatalf("end: %d %s", w.Code, w.Body)
	}
}

func claim(e *env, token, wallet string) (int, creditReply) {
	e.t.Helper()
	w := e.do("POST", "/d/"+token+"/credit", map[string]any{"wallet": wallet}, nil)
	var r creditReply
	json.Unmarshal(w.Body.Bytes(), &r)
	return w.Code, r
}

func TestCreditFlow(t *testing.T) {
	e, f := payEnv(t)
	a := e.create(tok1, "Ada Lovelace")

	// Still preparing: no credit yet, and the page has no credit card.
	if c, r := claim(e, a.Token, walletA); c != 409 || r.Outcome != "error" {
		t.Fatalf("preparing: %d %+v", c, r)
	}
	if w := e.do("GET", "/d/"+a.Token, nil, nil); strings.Contains(w.Body.String(), "Connect wallet") {
		t.Fatal("preparing page offers the credit")
	}
	e.endEmpty(tok1, a.ID)
	w := e.do("GET", "/d/"+a.Token, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Connect wallet") || !strings.Contains(w.Body.String(), "A gift for your project") || !strings.Contains(w.Body.String(), `<div class="big">$10</div>`) {
		t.Fatalf("empty page without the credit card: %d", w.Code)
	}

	// Bad addresses never reach Pay.
	for _, bad := range []string{"", "nope", walletA[:len(walletA)-1] + "7"} {
		if c, _ := claim(e, a.Token, bad); c != 400 {
			t.Fatalf("bad wallet %q: %d", bad, c)
		}
	}
	if f.n() != 0 {
		t.Fatal("pay was called for a bad address")
	}

	// Granted: one row, lowercase address, our external_ref, $10.
	c, r := claim(e, a.Token, walletA)
	if c != 200 || r.Outcome != "granted" || r.Wallet != strings.ToLower(walletA) || r.Amount != "$10" {
		t.Fatalf("grant: %d %+v", c, r)
	}
	if f.n() != 1 || len(f.last.Items) != 1 {
		t.Fatalf("pay calls %d items %d", f.n(), len(f.last.Items))
	}
	it := f.last.Items[0]
	if it.ExternalRef != testSignerAddr+":test:"+a.ID || it.AccountRef.Kind != "evm_address" ||
		it.AccountRef.ID != strings.ToLower(walletA) || it.AmountMicros != 10_000_000 || it.ExpiresAt != "" {
		t.Fatalf("row: %+v", it)
	}

	// Same session again: already, no second call, even with another wallet.
	if c, r := claim(e, a.Token, walletB); c != 200 || r.Outcome != "already" || r.Wallet != strings.ToLower(walletA) {
		t.Fatalf("second claim: %d %+v", c, r)
	}
	if f.n() != 1 {
		t.Fatal("pay called again for the same session")
	}
	w = e.do("GET", "/d/"+a.Token, nil, nil)
	if !strings.Contains(w.Body.String(), `<div id="reward">`) || !strings.Contains(w.Body.String(), `<div id="offer" hidden>`) ||
		!strings.Contains(w.Body.String(), "0xf39f…2266") || !strings.Contains(w.Body.String(), `href="https://pc.0g.ai"`) ||
		!strings.Contains(w.Body.String(), "valid until 13 October 2026") {
		t.Fatal("page does not show the credit as done")
	}

	// Second session, same wallet: fine (2 of 2). Third: over the limit, no call.
	b := e.create(tok1, "Ada Lovelace")
	e.endEmpty(tok1, b.ID)
	if c, r := claim(e, b.Token, strings.ToLower(walletA)); c != 200 || r.Outcome != "granted" {
		t.Fatalf("second wallet grant: %d %+v", c, r)
	}
	d := e.create(tok2, "Ada Lovelace")
	e.endEmpty(tok2, d.ID)
	if c, r := claim(e, d.Token, walletA); c != 409 || r.Outcome != "limit" || !strings.Contains(r.Message, "2 credits") {
		t.Fatalf("limit: %d %+v", c, r)
	}
	if f.n() != 2 {
		t.Fatalf("pay calls %d, want 2", f.n())
	}
	// ...but that session can still credit another wallet.
	if c, r := claim(e, d.Token, walletB); c != 200 || r.Outcome != "granted" {
		t.Fatalf("other wallet: %d %+v", c, r)
	}

	// Unknown token.
	if c, _ := claim(e, "nosuchtoken", walletB); c != 404 {
		t.Fatalf("unknown token: %d", c)
	}
}

func TestCreditPayFailures(t *testing.T) {
	e, f := payEnv(t)
	a := e.create(tok1, "Grace Hopper")
	e.endEmpty(tok1, a.ID)

	// Pay down: error, nothing counted; a retry works and is one more call.
	f.down = true
	if c, r := claim(e, a.Token, walletA); c != 502 || r.Outcome != "error" {
		t.Fatalf("down: %d %+v", c, r)
	}
	if n, _ := e.s.walletGrants(strings.ToLower(walletA)); n != 0 {
		t.Fatalf("error counted as a grant: %d", n)
	}
	f.down = false
	if c, r := claim(e, a.Token, walletA); c != 200 || r.Outcome != "granted" {
		t.Fatalf("retry: %d %+v", c, r)
	}

	// Rejected rows do not count towards the wallet cap and may be retried.
	b := e.create(tok1, "Grace Hopper")
	e.endEmpty(tok1, b.ID)
	f.reject = "lot_unusable"
	if c, r := claim(e, b.Token, walletB); c != 502 || r.Outcome != "rejected" || !strings.Contains(r.Message, "pool is empty") {
		t.Fatalf("rejected: %d %+v", c, r)
	}
	f.reject = ""
	if c, r := claim(e, b.Token, walletB); c != 200 || r.Outcome != "granted" {
		t.Fatalf("retry after reject: %d %+v", c, r)
	}
	if n, _ := e.s.walletGrants(strings.ToLower(walletB)); n != 1 {
		t.Fatalf("wallet B grants %d, want 1", n)
	}

	// Pay remembers a ref we lost: duplicate still counts as credited.
	e.s.db.Exec(`DELETE FROM credits WHERE session_id = ?`, b.ID)
	if c, r := claim(e, b.Token, walletB); c != 200 || r.Outcome != "granted" {
		t.Fatalf("duplicate: %d %+v", c, r)
	}
	if cr, err := e.s.creditBySession(b.ID); err != nil || cr.Outcome != "duplicate" {
		t.Fatalf("stored outcome: %+v %v", cr, err)
	}
}

func TestCreditDisabled(t *testing.T) {
	e := newEnv(t, nil)
	a := e.create(tok1, "Alan Turing")
	e.endEmpty(tok1, a.ID)
	if c, _ := claim(e, a.Token, walletA); c != 404 {
		t.Fatalf("disabled: %d", c)
	}
	if w := e.do("GET", "/d/"+a.Token, nil, nil); strings.Contains(w.Body.String(), "gift for your project") {
		t.Fatal("page offers credit while pay is off")
	}
}

func TestPayConfigNormalize(t *testing.T) {
	ok := PayConfig{APIURL: "https://pay.example/v1/regrants", SourceLotID: " " + strings.ToUpper(testLot), SignerKey: testSignerKey + " "}
	if err := ok.normalize(); err != nil {
		t.Fatal(err)
	}
	if ok.APIURL != "https://pay.example" || ok.SourceLotID != testLot || ok.AmountMicros != 10_000_000 ||
		ok.MaxPerWallet != 2 || ok.RefTag != "hackbox" || ok.amountUSD() != "$10" || ok.SpendURL != "https://pc.0g.ai" {
		t.Fatalf("defaults: %+v", ok)
	}
	for micros, want := range map[int64]string{2_500_000: "$2.50", 500_000: "$0.50", 10_000_000: "$10", 1_234_500: "$1.2345"} {
		if got := (PayConfig{AmountMicros: micros}).amountUSD(); got != want {
			t.Errorf("amountUSD(%d) = %s, want %s", micros, got, want)
		}
	}
	bad := []PayConfig{
		{APIURL: "pay.example", SourceLotID: testLot, SignerKey: testSignerKey},
		{APIURL: "https://pay.example", SourceLotID: "short", SignerKey: testSignerKey},
		{APIURL: "https://pay.example", SourceLotID: testLot, SignerKey: "0x12"},
		{APIURL: "https://pay.example", SourceLotID: testLot, SignerKey: testSignerKey, RefTag: "a:b"},
		{APIURL: "https://pay.example", SourceLotID: testLot, SignerKey: testSignerKey, ExpiresAt: "tomorrow"},
		{APIURL: "https://pay.example", SourceLotID: testLot, SignerKey: testSignerKey, SpendURL: "http://evil.example"},
		{APIURL: "https://pay.example"}, // partly filled in is a mistake, not "off"
	}
	for i, c := range bad {
		if err := c.normalize(); err == nil {
			t.Errorf("config %d accepted", i)
		}
	}
	var off PayConfig
	if err := off.normalize(); err != nil || off.enabled() {
		t.Fatal("empty pay config must be off")
	}
}

func TestShortLink(t *testing.T) {
	e := newEnv(t, nil)
	a := e.create(tok1, "Ada Lovelace")
	w := e.do("GET", "/c/"+strings.ToLower(a.Code), nil, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/d/"+a.Token {
		t.Fatalf("short link: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := e.do("GET", "/c/ZZZZZZ", nil, nil); w.Code != 404 || !strings.Contains(w.Body.String(), "No session with that code") {
		t.Fatalf("unknown code: %d", w.Code)
	}
	if w := e.do("GET", "/c/AB", nil, nil); w.Code != 400 {
		t.Fatalf("short code: %d", w.Code)
	}
	for i := 0; i < 5; i++ {
		e.do("GET", "/c/ZZZZZZ", nil, nil)
	}
	if w := e.do("GET", "/c/"+a.Code, nil, nil); w.Code != 429 {
		t.Fatalf("limiter: %d", w.Code)
	}
}

func TestClientIPBehindProxy(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.ProxyKey = "proxy-secret-0123456789" })
	_ = e
	mk := func(h map[string]string) *http.Request {
		r := httptest.NewRequest("GET", "/code", nil)
		r.RemoteAddr = "10.0.0.9:1234"
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	if ip := clientIP(mk(map[string]string{"Cf-Connecting-IP": "2a06:98c0:3600::103", "X-Hackbox-Proxy-Key": "proxy-secret-0123456789", "X-Hackbox-Client-IP": "122.11.212.18"})); ip != "122.11.212.18" {
		t.Fatalf("trusted proxy: %s", ip)
	}
	if ip := clientIP(mk(map[string]string{"Cf-Connecting-IP": "2a06:98c0:3600::103", "X-Hackbox-Proxy-Key": "wrong", "X-Hackbox-Client-IP": "122.11.212.18"})); ip != "2a06:98c0:3600::103" {
		t.Fatalf("wrong key must be ignored: %s", ip)
	}
	if ip := clientIP(mk(map[string]string{"X-Hackbox-Client-IP": "1.2.3.4"})); ip != "10.0.0.9" {
		t.Fatalf("no key must be ignored: %s", ip)
	}
	proxyKey = ""
	if ip := clientIP(mk(map[string]string{"X-Hackbox-Proxy-Key": "proxy-secret-0123456789", "X-Hackbox-Client-IP": "1.2.3.4"})); ip != "10.0.0.9" {
		t.Fatalf("feature off must be ignored: %s", ip)
	}
	bad := Config{PublicBaseURL: "https://x.example", ProxyKey: "short"}
	if err := bad.normalize(); err == nil {
		t.Fatal("short proxy_key accepted")
	}
}

// On the time-up screen (box state "ending" for this session) the reward opens while the
// files are still being saved; during the session it stays closed.
func TestCreditOnTimeUpScreen(t *testing.T) {
	e, _ := payEnv(t)
	a := e.create(tok1, "Grace Hopper")
	heartbeat(e, tok1, map[string]any{"state": "active", "session_id": a.ID})
	if c, _ := claim(e, a.Token, walletA); c != 409 {
		t.Fatalf("claim during the session: %d, want 409", c)
	}
	heartbeat(e, tok1, map[string]any{"state": "ending", "session_id": a.ID})
	w := e.do("GET", "/d/"+a.Token, nil, nil)
	if !strings.Contains(w.Body.String(), "preparing your files") || !strings.Contains(w.Body.String(), "Connect wallet") {
		t.Fatal("time-up screen: preparing page should offer the reward")
	}
	if strings.Contains(w.Body.String(), `http-equiv="refresh"`) {
		t.Fatal("preparing page with the reward must not reload by itself")
	}
	if c, r := claim(e, a.Token, walletA); c != 200 || r.Outcome != "granted" {
		t.Fatalf("claim on the time-up screen: %d %+v", c, r)
	}
}
