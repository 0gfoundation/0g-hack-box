package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Agent keys and agent choice, set from the dashboard and pulled by the boxes.
//
// Rows live in the config table under scope "*" (all boxes) or a box name.
// The effective config of a box is, per name, the box row, else the "*" row.
// Secret values never leave the hub except to a box through GET /api/v1/config.

const (
	cfgAnthropic = "anthropic-api-key"
	cfgRouter    = "0g-router-key"
	cfgAgents    = "agents"
	scopeAll     = "*"
)

// configNames are all names, in display order.
var configNames = []string{cfgAnthropic, cfgRouter, cfgAgents}

var secretNames = map[string]bool{cfgAnthropic: true, cfgRouter: true}

// knownAgents are the agents a box can offer, in canonical order.
var knownAgents = []string{"claude", "claude-0g", "opencode"}

func isConfigName(n string) bool { return n == cfgAgents || secretNames[n] }

// validateConfigValue checks a value and returns it normalised. Errors never
// quote the value, since it may be a secret.
func validateConfigValue(name, value string) (string, error) {
	switch {
	case secretNames[name]:
		if len(value) < 8 || len(value) > 512 {
			return "", fmt.Errorf("%s must be 8 to 512 characters", name)
		}
		for i := 0; i < len(value); i++ {
			if c := value[i]; c < 0x21 || c > 0x7e {
				return "", fmt.Errorf("%s must be printable ASCII without spaces", name)
			}
		}
		return value, nil
	case name == cfgAgents:
		list := strings.Fields(value)
		if len(list) == 0 {
			return "", errors.New("agents needs at least one of: " + strings.Join(knownAgents, ", "))
		}
		seen := map[string]bool{}
		for _, a := range list {
			ok := false
			for _, k := range knownAgents {
				if a == k {
					ok = true
				}
			}
			if !ok {
				return "", fmt.Errorf("unknown agent %q (use %s)", a, strings.Join(knownAgents, ", "))
			}
			if seen[a] {
				return "", fmt.Errorf("agent %q listed twice", a)
			}
			seen[a] = true
		}
		return strings.Join(list, " "), nil
	}
	return "", fmt.Errorf("unknown config name %q", name)
}

type configRow struct {
	Value     string
	UpdatedAt int64
}

// configRows returns scope -> name -> row.
func (s *Server) configRows() (map[string]map[string]configRow, error) {
	rows, err := s.db.Query(`SELECT scope, name, value, updated_at FROM config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]configRow{}
	for rows.Next() {
		var scope, name string
		var r configRow
		if err := rows.Scan(&scope, &name, &r.Value, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if out[scope] == nil {
			out[scope] = map[string]configRow{}
		}
		out[scope][name] = r
	}
	return out, rows.Err()
}

func effectiveFrom(all map[string]map[string]configRow, box string) map[string]string {
	eff := map[string]string{}
	for _, n := range configNames {
		if r, ok := all[box][n]; ok {
			eff[n] = r.Value
		} else if r, ok := all[scopeAll][n]; ok {
			eff[n] = r.Value
		}
	}
	return eff
}

// effectiveConfig is the config a box should run with.
func (s *Server) effectiveConfig(box string) (map[string]string, error) {
	all, err := s.configRows()
	if err != nil {
		return nil, err
	}
	return effectiveFrom(all, box), nil
}

// configVersion is the first 16 hex characters of sha256 over the sorted
// "name=value\n" lines, or "" when nothing is set.
func configVersion(eff map[string]string) string {
	if len(eff) == 0 {
		return ""
	}
	names := make([]string, 0, len(eff))
	for n := range eff {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s=%s\n", n, eff[n])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ---------------------------------------------------------------------------
// Box API

type boxConfigResp struct {
	Version string            `json:"version"`
	Secrets map[string]string `json:"secrets"`
	Agents  []string          `json:"agents,omitempty"`
}

func (s *Server) apiConfig(w http.ResponseWriter, r *http.Request, box string) {
	eff, err := s.effectiveConfig(box)
	if err != nil {
		s.serverError(w, err)
		return
	}
	resp := boxConfigResp{Version: configVersion(eff), Secrets: map[string]string{}}
	for n, v := range eff {
		if secretNames[n] {
			resp.Secrets[n] = v
		}
	}
	if a, ok := eff[cfgAgents]; ok {
		resp.Agents = strings.Fields(a)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Dashboard

type dashConfigName struct {
	Set       bool  `json:"set"`
	UpdatedAt int64 `json:"updated_at"`
	Inherited bool  `json:"inherited"`
}

type dashConfigScope struct {
	Scope           string                    `json:"scope"`
	Names           map[string]dashConfigName `json:"names"`
	Agents          []string                  `json:"agents"`
	EffectiveAgents []string                  `json:"effective_agents"`
	ConfigVersion   string                    `json:"config_version"`
}

type dashConfigResp struct {
	KnownAgents []string          `json:"known_agents"`
	Scopes      []dashConfigScope `json:"scopes"`
}

func (s *Server) dashConfigState() (dashConfigResp, error) {
	all, err := s.configRows()
	if err != nil {
		return dashConfigResp{}, err
	}
	boxes := s.allBoxNames()
	resp := dashConfigResp{KnownAgents: knownAgents, Scopes: []dashConfigScope{}}
	for _, scope := range append([]string{scopeAll}, boxes...) {
		sc := dashConfigScope{Scope: scope, Names: map[string]dashConfigName{}, Agents: []string{}, EffectiveAgents: []string{}}
		for _, n := range configNames {
			var d dashConfigName
			if r, ok := all[scope][n]; ok {
				d.Set, d.UpdatedAt = true, r.UpdatedAt
				if n == cfgAgents {
					sc.Agents = strings.Fields(r.Value)
				}
			} else if r, ok := all[scopeAll][n]; ok && scope != scopeAll {
				d.Inherited, d.UpdatedAt = true, r.UpdatedAt
			}
			sc.Names[n] = d
		}
		eff := effectiveFrom(all, scope)
		if a, ok := eff[cfgAgents]; ok {
			sc.EffectiveAgents = strings.Fields(a)
		}
		if scope != scopeAll {
			sc.ConfigVersion = configVersion(eff)
		}
		resp.Scopes = append(resp.Scopes, sc)
	}
	return resp, nil
}

func (s *Server) dashConfigGet(w http.ResponseWriter, r *http.Request) {
	resp, err := s.dashConfigState()
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type dashConfigPost struct {
	Scope string            `json:"scope"`
	Set   map[string]string `json:"set"`
	Clear []string          `json:"clear"`
}

func (s *Server) dashConfigSet(w http.ResponseWriter, r *http.Request) {
	var req dashConfigPost
	if !readJSON(w, r, &req) {
		return
	}
	if req.Scope != scopeAll && !s.knownBox(req.Scope) {
		jsonError(w, http.StatusBadRequest, "scope must be \"*\" or a configured box name")
		return
	}
	set := map[string]string{}
	for n, v := range req.Set {
		if !isConfigName(n) {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("unknown config name %q", n))
			return
		}
		if v == "" {
			continue // an empty input means "leave as is"; use clear to remove
		}
		nv, err := validateConfigValue(n, v)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		set[n] = nv
	}
	for _, n := range req.Clear {
		if !isConfigName(n) {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("unknown config name %q", n))
			return
		}
		if _, both := set[n]; both {
			jsonError(w, http.StatusBadRequest, n+" is both set and cleared")
			return
		}
	}
	if len(set) == 0 && len(req.Clear) == 0 {
		jsonError(w, http.StatusBadRequest, "nothing to set or clear")
		return
	}
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer tx.Rollback()
	for n, v := range set {
		if _, err := tx.Exec(`INSERT INTO config (scope, name, value, updated_at) VALUES (?,?,?,?)
			ON CONFLICT(scope, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			req.Scope, n, v, now); err != nil {
			s.serverError(w, err)
			return
		}
	}
	for _, n := range req.Clear {
		if _, err := tx.Exec(`DELETE FROM config WHERE scope = ? AND name = ?`, req.Scope, n); err != nil {
			s.serverError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, err)
		return
	}
	changed := make([]string, 0, len(set)+len(req.Clear))
	for n := range set {
		changed = append(changed, "set "+n)
	}
	for _, n := range req.Clear {
		changed = append(changed, "clear "+n)
	}
	sort.Strings(changed)
	s.log.Printf("config: scope %s: %s", req.Scope, strings.Join(changed, ", ")) // names only, never values
	resp, err := s.dashConfigState()
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// boxConfigVersions returns box -> wanted config version for every box.
func (s *Server) boxConfigVersions(boxes []string) (map[string]string, error) {
	all, err := s.configRows()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, b := range boxes {
		out[b] = configVersion(effectiveFrom(all, b))
	}
	return out, nil
}
