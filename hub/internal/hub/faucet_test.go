package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testFaucetKey = "fsk_test_0123456789abcdef"

// fakeFaucet is the faucet's /v1 API: idempotent on request_id, one drip per wallet per
// "day" (until reset), transfers complete on the second status read.
type fakeFaucet struct {
	t       *testing.T
	mu      sync.Mutex
	posts   int
	gets    int
	byReq   map[string]map[string]any
	byID    map[string]map[string]any
	reads   map[string]int
	walletN map[string]int
	last    faucetTransferReq
	down    bool   // answer 503
	fail    bool   // new transfers fail on the chain
	amount  string // the amount the faucet reports; default "0.5"
	nextID  int
}

func newFakeFaucet(t *testing.T) (*fakeFaucet, *httptest.Server) {
	f := &fakeFaucet{t: t, byReq: map[string]map[string]any{}, byID: map[string]map[string]any{},
		reads: map[string]int{}, walletN: map[string]int{}, amount: "0.5"}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeFaucet) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+testFaucetKey {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"missing or invalid authorization header"}`)
		return
	}
	if f.down {
		w.WriteHeader(503)
		io.WriteString(w, "upstream down")
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/transfers":
		f.posts++
		var req faucetTransferReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID == "" || req.UserID == "" {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid request body"}`)
			return
		}
		if len(req.RequestID) > 80 {
			f.t.Errorf("request_id longer than 80: %q", req.RequestID)
		}
		f.last = req
		if tr, ok := f.byReq[req.RequestID]; ok {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(tr)
			return
		}
		if f.walletN[req.Wallet] >= 1 && req.PromoCode == "" {
			w.WriteHeader(429)
			io.WriteString(w, `{"status":"ratelimited","error":"rate limit exceeded","available_after":"2026-10-02T14:05:00Z"}`)
			return
		}
		f.walletN[req.Wallet]++
		f.nextID++
		id := "00000000-0000-0000-0000-00000000000" + string(rune('0'+f.nextID))
		tr := map[string]any{"transfer_id": id, "request_id": req.RequestID, "status": "queued", "amount": f.amount,
			"asset": "OG", "network": "0g-testnet", "tx_hash": nil, "failure_reason": nil}
		if f.fail {
			tr["status"], tr["failure_reason"] = "failed", "insufficient faucet balance"
		}
		f.byReq[req.RequestID], f.byID[id] = tr, tr
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(tr)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/transfers/"):
		f.gets++
		id := strings.TrimPrefix(r.URL.Path, "/v1/transfers/")
		tr, ok := f.byID[id]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"transfer not found"}`)
			return
		}
		f.reads[id]++
		if tr["status"] == "queued" {
			tr["status"], tr["tx_hash"] = "executing", "0xabc"+id[len(id)-1:]
		} else if tr["status"] == "executing" {
			tr["status"] = "completed"
		}
		json.NewEncoder(w).Encode(tr)
	default:
		f.t.Errorf("faucet: unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

func (f *fakeFaucet) counts() (int, int) { f.mu.Lock(); defer f.mu.Unlock(); return f.posts, f.gets }

func faucetEnv(t *testing.T, mod func(*FaucetConfig)) (*env, *fakeFaucet) {
	ff, srv := newFakeFaucet(t)
	e := newEnv(t, func(o *Options) {
		o.Config.Faucet = FaucetConfig{APIURL: srv.URL, APIKey: testFaucetKey}
		if mod != nil {
			mod(&o.Config.Faucet)
		}
	})
	return e, ff
}

// startActive creates a session on a box and reports it active.
func (e *env) startActive(tok string) createSessionResp {
	e.t.Helper()
	a := e.create(tok, "Ada Lovelace")
	heartbeat(e, tok, map[string]any{"session_id": a.ID, "state": "active", "seconds_left": 1500})
	return a
}

func fund(e *env, token, wallet string) (int, fundReply) {
	e.t.Helper()
	w := e.do("POST", "/d/"+token+"/fund", map[string]any{"wallet": wallet}, nil)
	var r fundReply
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		e.t.Fatalf("fund reply %q: %v", w.Body.String(), err)
	}
	return w.Code, r
}

func fundStatus(e *env, token, query string) (int, fundList) {
	e.t.Helper()
	w := e.do("GET", "/d/"+token+"/fund"+query, nil, nil)
	var l fundList
	json.Unmarshal(w.Body.Bytes(), &l)
	return w.Code, l
}

func TestFundFlow(t *testing.T) {
	e, ff := faucetEnv(t, nil)
	a := e.startActive(tok1)
	lowA := strings.ToLower(walletA)

	// Bad addresses never reach the faucet.
	for _, bad := range []string{"", "nope", walletA[:len(walletA)-1] + "7"} {
		if c, r := fund(e, a.Token, bad); c != 400 || r.Outcome != "error" {
			t.Fatalf("bad wallet %q: %d %+v", bad, c, r)
		}
	}
	if p, _ := ff.counts(); p != 0 {
		t.Fatal("faucet called for a bad address")
	}

	// Queued: our request_id, user_id and metadata, no promo code.
	c, r := fund(e, a.Token, walletA)
	if c != 202 || r.Outcome != "queued" || r.Status != "queued" || r.Wallet != lowA || r.Amount != "0.5" || r.Remaining != "0.5" {
		t.Fatalf("fund: %d %+v", c, r)
	}
	wantRID := "hackbox:" + a.ID + ":" + lowA
	if ff.last.RequestID != wantRID || ff.last.UserID != wantRID || ff.last.PromoCode != "" || ff.last.Metadata["box"] != "hackbox1" {
		t.Fatalf("faucet request: %+v", ff.last)
	}

	// The same call again: the same transfer, no second POST.
	e.clk.Add(3 * time.Second)
	c, r = fund(e, a.Token, walletA)
	if p, _ := ff.counts(); p != 1 || c != 200 || r.Outcome != "executing" || r.TxHash != "0xabc1" ||
		r.ExplorerURL != "https://chainscan-galileo.0g.ai/tx/0xabc1" {
		t.Fatalf("repeat: posts %d, %d %+v", p, c, r)
	}

	// Status: completed on the next read, at most one faucet read per 2 s.
	e.clk.Add(3 * time.Second)
	code, l := fundStatus(e, a.Token, "")
	if code != 200 || !l.Active || len(l.Transfers) != 1 || l.Transfers[0].Status != "completed" || l.Remaining != "0.5" ||
		l.Amount != "0.5" || l.SessionOG != "1" {
		t.Fatalf("status: %d %+v", code, l)
	}
	_, g := ff.counts()
	fundStatus(e, a.Token, "?wallet="+walletA)
	if _, g2 := ff.counts(); g2 != g {
		t.Fatal("completed transfer was read again from the faucet")
	}
	if c, r := fund(e, a.Token, walletA); c != 200 || r.Outcome != "already" || !strings.Contains(r.Message, "already funded") {
		t.Fatalf("after completion: %d %+v", c, r)
	}

	// A second wallet in the same session: 0.5 more, then the session is at 1 0G.
	if c, r := fund(e, a.Token, walletB); c != 202 || r.Remaining != "0" {
		t.Fatalf("second wallet: %d %+v", c, r)
	}
	walletC := "0x90f79bf6eb2c4f870365e785982e1f101e93b906"
	if c, r := fund(e, a.Token, walletC); c != 409 || r.Outcome != "limit" || !strings.Contains(r.Message, "1 0G per session") {
		t.Fatalf("session cap: %d %+v", c, r)
	}
	if p, _ := ff.counts(); p != 2 {
		t.Fatalf("posts %d, want 2", p)
	}

	// Unknown token.
	if c, _ := fund(e, "nosuchtoken", walletA); c != 404 {
		t.Fatalf("unknown token: %d", c)
	}
}

func TestFundOnlyWhileActive(t *testing.T) {
	e, ff := faucetEnv(t, nil)
	a := e.create(tok1, "Grace Hopper")
	// Created but the box has not reported it active yet.
	if c, r := fund(e, a.Token, walletA); c != 403 || r.Outcome != "inactive" {
		t.Fatalf("before active: %d %+v", c, r)
	}
	heartbeat(e, tok1, map[string]any{"session_id": a.ID, "state": "active", "seconds_left": 1500})
	// The box stopped checking in: no longer counts as active.
	e.clk.Add(31 * time.Second)
	if c, r := fund(e, a.Token, walletA); c != 403 || r.Outcome != "inactive" {
		t.Fatalf("stale box: %d %+v", c, r)
	}
	heartbeat(e, tok1, map[string]any{"session_id": a.ID, "state": "active", "seconds_left": 1400})
	if c, _ := fund(e, a.Token, walletA); c != 202 {
		t.Fatalf("active: %d", c)
	}
	// Ending: the box reports "ending", then the session ends.
	heartbeat(e, tok1, map[string]any{"session_id": a.ID, "state": "ending"})
	if c, r := fund(e, a.Token, walletB); c != 403 || r.Outcome != "inactive" {
		t.Fatalf("ending: %d %+v", c, r)
	}
	e.endEmpty(tok1, a.ID)
	heartbeat(e, tok1, map[string]any{"state": "idle"})
	if c, _ := fund(e, a.Token, walletB); c != 403 {
		t.Fatalf("ended: %d", c)
	}
	// Status still readable after the session; the earlier transfer is reported, not resent.
	if code, l := fundStatus(e, a.Token, ""); code != 200 || l.Active || len(l.Transfers) != 1 {
		t.Fatalf("status after end: %d %+v", code, l)
	}
	if c, r := fund(e, a.Token, walletA); c != 200 || r.Wallet != strings.ToLower(walletA) {
		t.Fatalf("existing after end: %d %+v", c, r)
	}
	if p, _ := ff.counts(); p != 1 {
		t.Fatalf("posts %d, want 1", p)
	}
	// Another box's active session does not make this one active.
	b := e.startActive(tok2)
	_ = b
	if c, _ := fund(e, a.Token, walletB); c != 403 {
		t.Fatalf("other box active: %d", c)
	}
}

func TestFundCapsAcrossSessions(t *testing.T) {
	e, _ := faucetEnv(t, func(f *FaucetConfig) {
		f.PromoCode = "BB-TEST01" // no 24 h wallet limit in the fake: the hub's caps are what stop it
		f.AmountOG = 0.5
		f.WalletMaxOG = 1
		f.BoxHourlyMaxOG = 1.5
		f.EventBudgetOG = 2.5
	})
	walletC := "0x90f79bf6eb2c4f870365e785982e1f101e93b906"
	walletD := "0x15d34aaf54267db7d7c367839aaf71a00a2c6a65"
	walletE := "0x9965507d1a55bcc2695c58ba16fb37d819b0a4dc"

	// The session, end, repeat loop on one box: per wallet, then per box per hour.
	s1 := e.startActive(tok1)
	if c, r := fund(e, s1.Token, walletA); c != 202 {
		t.Fatalf("s1: %d %+v", c, r)
	}
	e.endEmpty(tok1, s1.ID)
	s2 := e.startActive(tok1)
	if c, _ := fund(e, s2.Token, walletA); c != 202 {
		t.Fatalf("s2 wallet A second 0.5: %d", c)
	}
	e.endEmpty(tok1, s2.ID)
	s3 := e.startActive(tok1)
	if c, r := fund(e, s3.Token, walletA); c != 409 || !strings.Contains(r.Message, "1 0G from hack-box sessions") {
		t.Fatalf("wallet cap: %d %+v", c, r)
	}
	if c, _ := fund(e, s3.Token, walletB); c != 202 {
		t.Fatalf("s3 wallet B: %d", c)
	}
	if c, r := fund(e, s3.Token, walletC); c != 409 || !strings.Contains(r.Message, "hourly allowance of 1.5 0G") {
		t.Fatalf("box hourly cap: %d %+v", c, r)
	}
	// An hour later the box may send again.
	e.clk.Add(61 * time.Minute)
	heartbeat(e, tok1, map[string]any{"session_id": s3.ID, "state": "active", "seconds_left": 100})
	if c, r := fund(e, s3.Token, walletC); c != 202 {
		t.Fatalf("after an hour: %d %+v", c, r)
	}
	// Event budget: 2 0G used, 2.5 allowed.
	s4 := e.startActive(tok2)
	if c, _ := fund(e, s4.Token, walletD); c != 202 {
		t.Fatalf("s4 D: %d", c)
	}
	if c, r := fund(e, s4.Token, walletE); c != 409 || !strings.Contains(r.Message, "event's testnet 0G budget (2.5 0G)") {
		t.Fatalf("event cap: %d %+v", c, r)
	}
}

func TestFundFaucetAnswers(t *testing.T) {
	e, ff := faucetEnv(t, nil)
	a := e.startActive(tok1)

	// Down: unknown outcome, counted; a retry reuses the request_id.
	ff.down = true
	if c, r := fund(e, a.Token, walletA); c != 502 || r.Outcome != "unknown" || !strings.Contains(r.Message, "never sends twice") {
		t.Fatalf("down: %d %+v", c, r)
	}
	if n, _ := e.s.fundedSum(0, "1 = 1"); n != 500 {
		t.Fatalf("unknown outcome must hold its amount: %d", n)
	}
	ff.down = false
	if c, r := fund(e, a.Token, walletA); c != 202 || r.Outcome != "queued" {
		t.Fatalf("retry: %d %+v", c, r)
	}
	if ff.last.RequestID != "hackbox:"+a.ID+":"+strings.ToLower(walletA) {
		t.Fatalf("retry used another request_id: %s", ff.last.RequestID)
	}

	// 429: the wallet had a drip in the last 24 h (here: walletA, just now, in another session).
	b := e.startActive(tok2)
	c, r := fund(e, b.Token, walletA)
	if c != 429 || r.Outcome != "refused" || !strings.Contains(r.Message, "last 24 hours") || !strings.Contains(r.Message, "14:05 UTC on 2 Oct") {
		t.Fatalf("429: %d %+v", c, r)
	}
	if n, _ := e.s.fundedSum(0, "session_id = ?", b.ID); n != 0 {
		t.Fatalf("refused counted: %d", n)
	}

	// Failed on the chain: not counted, and a retry uses a new request_id.
	ff.fail = true
	if c, r := fund(e, b.Token, walletB); c != 502 || r.Outcome != "failed" || !strings.Contains(r.Message, "insufficient faucet balance") {
		t.Fatalf("failed: %d %+v", c, r)
	}
	ff.fail = false
	// The real faucet keeps the wallet's 24 h slot after a failed send, so the retry (with a
	// fresh request_id) is refused there; the hub only makes sure it can be asked again.
	if c, r := fund(e, b.Token, walletB); c != 429 || r.Outcome != "refused" {
		t.Fatalf("after failure: %d %+v", c, r)
	}
	if !strings.HasSuffix(ff.last.RequestID, ":2") {
		t.Fatalf("second attempt request_id: %s", ff.last.RequestID)
	}

	// A wrong key: refused with a staff message, nothing counted.
	e.s.cfg.Faucet.APIKey = "fsk_wrong"
	walletC := "0x90f79bf6eb2c4f870365e785982e1f101e93b906"
	if c, r := fund(e, b.Token, walletC); c != 502 || r.Outcome != "refused" || !strings.Contains(r.Message, "staff") {
		t.Fatalf("bad key: %d %+v", c, r)
	}
}

func TestFundPromoAmount(t *testing.T) {
	e, ff := faucetEnv(t, func(f *FaucetConfig) { f.PromoCode = "BB-ONE0G"; f.AmountOG = 1 })
	ff.amount = "1"
	a := e.startActive(tok1)
	c, r := fund(e, a.Token, walletA)
	if c != 202 || r.Amount != "1" || r.Remaining != "0" || ff.last.PromoCode != "BB-ONE0G" {
		t.Fatalf("promo: %d %+v %+v", c, r, ff.last)
	}
	if c, r := fund(e, a.Token, walletB); c != 409 || r.Outcome != "limit" {
		t.Fatalf("one 0G per session: %d %+v", c, r)
	}
}

func TestFundRateLimitAndDisabled(t *testing.T) {
	e, _ := faucetEnv(t, func(f *FaucetConfig) { f.IPPerMinute = 3 })
	a := e.startActive(tok1)
	for i := 0; i < 3; i++ {
		fund(e, a.Token, "bad")
	}
	if c, r := fund(e, a.Token, walletA); c != 429 || !strings.Contains(r.Message, "Too many") {
		t.Fatalf("ip limit: %d %+v", c, r)
	}
	e.clk.Add(61 * time.Second)
	heartbeat(e, tok1, map[string]any{"session_id": a.ID, "state": "active", "seconds_left": 100})
	if c, _ := fund(e, a.Token, walletA); c != 202 {
		t.Fatalf("after a minute: %d", c)
	}

	off := newEnv(t, nil)
	b := off.startActive(tok1)
	if w := off.do("POST", "/d/"+b.Token+"/fund", map[string]any{"wallet": walletA}, nil); w.Code != 404 {
		t.Fatalf("disabled POST: %d", w.Code)
	}
	if w := off.do("GET", "/d/"+b.Token+"/fund", nil, nil); w.Code != 404 {
		t.Fatalf("disabled GET: %d", w.Code)
	}
}

func TestFaucetConfigNormalize(t *testing.T) {
	ok := FaucetConfig{APIKey: " " + testFaucetKey + "\n"}
	if err := ok.normalize(); err != nil {
		t.Fatal(err)
	}
	if ok.APIURL != "https://faucet-api.udhaykumarbala.dev" || ok.AmountOG != 0.5 || ok.SessionMaxOG != 1 || ok.WalletMaxOG != 1 ||
		ok.BoxHourlyMaxOG != 2 || ok.EventBudgetOG != 50 || ok.IPPerMinute != 30 || ok.RefTag != "hackbox" || ok.APIKey != testFaucetKey {
		t.Fatalf("defaults: %+v", ok)
	}
	bad := []FaucetConfig{
		{APIKey: testFaucetKey, APIURL: "faucet.example"},
		{APIKey: testFaucetKey, RefTag: "a:b"},
		{APIKey: testFaucetKey, RefTag: strings.Repeat("x", 17)},
		{APIKey: testFaucetKey, EventSince: "today"},
		{APIKey: testFaucetKey, AmountOG: 1, SessionMaxOG: 0.5},
	}
	for i, c := range bad {
		if err := c.normalize(); err == nil {
			t.Errorf("config %d accepted", i)
		}
	}
	var off FaucetConfig
	if err := off.normalize(); err != nil || off.enabled() {
		t.Fatal("empty faucet config must be off")
	}
	for m, want := range map[int64]string{500: "0.5", 1000: "1", 2500: "2.5", 1: "0.001", 0: "0"} {
		if got := fmtOG(m); got != want {
			t.Errorf("fmtOG(%d) = %s, want %s", m, got, want)
		}
	}
}

func TestFundLogHidesToken(t *testing.T) {
	e, _ := faucetEnv(t, nil)
	a := e.startActive(tok1)
	fund(e, a.Token, walletA)
	if strings.Contains(e.log.String(), a.Token) || strings.Contains(e.log.String(), testFaucetKey) {
		t.Fatal("log holds the download token or the faucet key")
	}
}
