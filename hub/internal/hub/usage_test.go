package hub

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestParseUsage(t *testing.T) {
	u := parseUsage([]byte(`{"input":10,"output":5,"reasoning":2,"cache_read":100,"cache_write":0,"replies":3,
		"models":{"0gm-1.0-35b-a3b":17},"extra":"ignored"}`))
	if u == nil || u.Input != 10 || u.Output != 5 || u.Reasoning != 2 || u.CacheRead != 100 || u.Replies != 3 ||
		u.Models["0gm-1.0-35b-a3b"] != 17 {
		t.Fatalf("parse: %+v", u)
	}
	for _, bad := range []string{``, `null`, `[]`, `"x"`, `{"input":-1}`, `{"input":1.5}`, `{"output":"7"}`,
		`{"input":1e16}`, `{"models":{"m":-3}}`, `{"models":[1]}`, `{"replies":true}`,
		`{"models":{"m":1},"pad":"` + strings.Repeat("x", usageMaxBytes) + `"}`} {
		if u := parseUsage([]byte(bad)); u != nil {
			t.Errorf("%.40s: accepted %+v", bad, u)
		}
	}
	// Model names are cleaned and capped in number.
	var sb strings.Builder
	sb.WriteString(`{"models":{`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"m` + itoa(int64(i)) + `\u0007":1`)
	}
	sb.WriteString(`}}`)
	u = parseUsage([]byte(sb.String()))
	if u == nil || len(u.Models) != usageMaxModels {
		t.Fatalf("models cap: %+v", u)
	}
	for k := range u.Models {
		if strings.ContainsRune(k, 7) {
			t.Fatalf("control character kept in %q", k)
		}
	}
}

func TestUsageHeartbeatKeepsMax(t *testing.T) {
	e := newEnv(t, nil)
	a := e.create(tok1, "Ada Lovelace")
	e.create(tok2, "Grace Hopper") // no usage: not counted
	hb := func(u any) {
		heartbeat(e, tok1, map[string]any{"session_id": a.ID, "state": "active", "seconds_left": 600, "usage": u})
	}
	hb(map[string]any{"input": 100, "output": 40, "reasoning": 5, "cache_read": 900, "cache_write": 1, "replies": 2,
		"models": map[string]any{"0gm-1.0-35b-a3b": 145}})
	// A lower count (a partial copy) never lowers the stored one; a higher one raises it.
	hb(map[string]any{"input": 80, "output": 60, "reasoning": 0, "cache_read": 0, "cache_write": 0, "replies": 1,
		"models": map[string]any{"0gm-1.0-35b-a3b": 140, "other": 3}})
	// Malformed usage is ignored and the heartbeat still works.
	hb(map[string]any{"input": -5})
	hb("garbage")
	w := e.do("POST", "/api/v1/heartbeat", `{"state":"active","session_id":"`+a.ID+`","usage":{"input":"1"}}`, box(tok1))
	if w.Code != 200 {
		t.Fatalf("malformed usage failed the heartbeat: %d", w.Code)
	}
	st := dashState(e)
	u := findBox(st, "hackbox1").Usage
	if u == nil || u.Input != 100 || u.Output != 60 || u.Reasoning != 5 || u.CacheRead != 900 || u.Replies != 2 ||
		u.Models["0gm-1.0-35b-a3b"] != 145 || u.Models["other"] != 3 {
		t.Fatalf("box usage: %+v", u)
	}
	if findBox(st, "hackbox2").Usage != nil {
		t.Fatal("box without usage shows some")
	}
	if tt := st.UsageTotals; tt.Input != 100 || tt.Output != 60 || tt.Sessions != 1 || tt.Replies != 2 {
		t.Fatalf("totals: %+v", tt)
	}

	// Usage in the end call counts too; a foreign session's usage is ignored.
	w = e.do("POST", "/api/v1/sessions/"+a.ID+"/end", map[string]any{"ended_at": e.clk.Now().Unix(), "reason": "time_up",
		"empty": true, "usage": map[string]any{"input": 150, "output": 60, "replies": 4}}, box(tok1))
	if w.Code != 200 {
		t.Fatalf("end: %d %s", w.Code, w.Body)
	}
	heartbeat(e, tok2, map[string]any{"session_id": a.ID, "state": "active", "usage": map[string]any{"input": 1 << 40}})
	st = dashState(e)
	var hist *dashSession
	for i := range st.Sessions {
		if st.Sessions[i].ID == a.ID {
			hist = &st.Sessions[i]
		}
	}
	if hist == nil || hist.Usage == nil || hist.Usage.Input != 150 || hist.Usage.Replies != 4 || hist.Usage.Reasoning != 5 {
		t.Fatalf("history usage: %+v", hist)
	}
	if hist.Fundings == nil || len(hist.Fundings) != 0 || hist.Reward != nil {
		t.Fatalf("history fundings/reward: %+v %+v", hist.Fundings, hist.Reward)
	}
}

func TestDashFundingsAndRewards(t *testing.T) {
	e, _ := faucetEnv(t, nil)
	e.s.cfg.Pay = PayConfig{AmountMicros: 10_000_000} // only for the totals' per-claim label
	a := e.create(tok1, "Ada Lovelace")
	b := e.create(tok2, "=Mallory")
	hash := "0x" + strings.Repeat("ab", 32)
	now := e.clk.Now().Unix()
	for _, f := range []*funding{
		{SessionID: a.ID, Box: "hackbox1", Wallet: "0x1111111111111111111111111111111111111111", RequestID: "r1",
			Status: "completed", AmountMilli: 500, TxHash: hash, CreatedAt: now},
		{SessionID: a.ID, Box: "hackbox1", Wallet: "0x2222222222222222222222222222222222222222", RequestID: "r2",
			Status: "queued", AmountMilli: 500, CreatedAt: now + 1},
		{SessionID: b.ID, Box: "hackbox2", Wallet: "0x3333333333333333333333333333333333333333", RequestID: "r3",
			Status: "refused", AmountMilli: 500, TxHash: "<script>", CreatedAt: now + 2},
	} {
		if err := e.s.saveFunding(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []credit{
		{SessionID: a.ID, Wallet: "0x1111111111111111111111111111111111111111", ExternalRef: "x1", AmountMicros: 10_000_000, Outcome: "granted", CreatedAt: now},
		{SessionID: b.ID, Wallet: "0x3333333333333333333333333333333333333333", ExternalRef: "x2", AmountMicros: 10_000_000, Outcome: "rejected", Detail: "lot_unusable", CreatedAt: now + 5},
	} {
		if err := e.s.saveCredit(c); err != nil {
			t.Fatal(err)
		}
	}
	st := dashState(e)
	if len(st.Fundings) != 3 || st.Fundings[0].Wallet != "0x3333333333333333333333333333333333333333" {
		t.Fatalf("fundings newest first: %+v", st.Fundings)
	}
	f0 := st.Fundings[2]
	if f0.TxURL != "https://chainscan-galileo.0g.ai/tx/"+hash || f0.Attendee != "Ada Lovelace" || f0.AmountOG != "0.5" || !f0.Counted {
		t.Fatalf("funding row: %+v", f0)
	}
	if st.Fundings[0].TxURL != "" || st.Fundings[0].Counted {
		t.Fatalf("refused row: %+v", st.Fundings[0])
	}
	ft := st.FundingTotals
	if !ft.Enabled || ft.FundedMilli != 1000 || ft.FundedOG != "1" || ft.CompletedOG != "0.5" || ft.BudgetOG != "50" ||
		ft.Transfers != 3 || ft.Wallets != 2 {
		t.Fatalf("funding totals: %+v", ft)
	}
	if len(st.Rewards) != 2 || st.Rewards[0].Outcome != "rejected" || st.Rewards[1].Box != "hackbox1" || !st.Rewards[1].Credited ||
		st.Rewards[1].AmountUSD != "$10" {
		t.Fatalf("rewards: %+v", st.Rewards)
	}
	if rt := st.RewardTotals; rt.GrantedUSD != "$10" || rt.Granted != 1 || rt.Claims != 2 {
		t.Fatalf("reward totals: %+v", rt)
	}
	for _, s := range st.Sessions {
		switch s.ID {
		case a.ID:
			if len(s.Fundings) != 2 || s.Reward == nil || !s.Reward.Credited || s.Fundings[1].TxURL == "" {
				t.Fatalf("session a: %+v %+v", s.Fundings, s.Reward)
			}
		case b.ID:
			if len(s.Fundings) != 1 || s.Reward == nil || s.Reward.Credited {
				t.Fatalf("session b: %+v %+v", s.Fundings, s.Reward)
			}
		}
	}

	// CSV exports: all rows, formulas defused.
	w := e.do("GET", "/dash/fundings.csv", nil, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("fundings.csv: %d %s", w.Code, w.Header())
	}
	recs, err := csv.NewReader(w.Body).ReadAll()
	if err != nil || len(recs) != 4 || recs[0][6] != "wallet" || recs[1][3] != "'=Mallory" {
		t.Fatalf("fundings.csv: %v %q", err, recs)
	}
	w = e.do("GET", "/dash/rewards.csv", nil, nil)
	recs, err = csv.NewReader(w.Body).ReadAll()
	if w.Code != 200 || err != nil || len(recs) != 3 || recs[0][8] != "outcome" || recs[2][8] != "granted" {
		t.Fatalf("rewards.csv: %d %v %q", w.Code, err, recs)
	}

	// Same guards as the rest of /dash: not through the tunnel, not from a box.
	for _, p := range []string{"/dash/fundings.csv", "/dash/rewards.csv"} {
		if w := e.do("GET", p, nil, hdr{"Cf-Connecting-IP": "1.2.3.4"}); w.Code != 404 {
			t.Errorf("%s through the tunnel: %d", p, w.Code)
		}
		if w := e.do("GET", p, nil, hdr{"X-Test-Remote": "192.0.2.10:5555"}); w.Code != 403 {
			t.Errorf("%s from a box: %d", p, w.Code)
		}
	}
}
