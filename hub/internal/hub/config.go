package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the hub's JSON config file.
type Config struct {
	PublicBaseURL string            `json:"public_base_url"`
	Boxes         map[string]string `json:"boxes"` // box token -> box name
	GitHub        GitHubConfig      `json:"github"`
	DownloadDays  int               `json:"download_days"`
}

// GitHubConfig names the org that receives one private repo per session.
// An empty token turns the push off (github_status "disabled").
type GitHubConfig struct {
	Org   string `json:"org"`
	Token string `json:"token"`
}

// LoadConfig reads and checks the config file.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, c.normalize()
}

func (c *Config) normalize() error {
	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	if c.PublicBaseURL == "" {
		return fmt.Errorf("config: public_base_url is required")
	}
	if c.DownloadDays <= 0 {
		c.DownloadDays = 7
	}
	if c.GitHub.Org == "" {
		c.GitHub.Org = "0g-hackbox-sessions"
	}
	if c.Boxes == nil {
		c.Boxes = map[string]string{}
	}
	for tok, name := range c.Boxes {
		if len(tok) < 8 {
			return fmt.Errorf("config: token for box %q is shorter than 8 characters", name)
		}
		if name == "" {
			return fmt.Errorf("config: a box token has an empty name")
		}
	}
	return nil
}

// boxNames returns the configured box names, unique.
func (c *Config) boxNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range c.Boxes {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
