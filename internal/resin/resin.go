package resin

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/cespare/xxhash/v2"
)

const AccountHeader = "X-Resin-Account"

type Config struct {
	URL      string
	Platform string
	Mode     string
}

type RuntimeConfig struct {
	ResinURL    string
	Platform    string
	ProxyOrigin string
	Token       string
}

func ParseRuntime(cfg Config) (*RuntimeConfig, error) {
	urlText := strings.TrimSpace(strings.TrimRight(cfg.URL, "/"))
	if urlText == "" {
		return nil, nil
	}
	if strings.TrimSpace(cfg.Platform) == "" {
		return nil, fmt.Errorf("resin platform required")
	}
	if strings.Contains(cfg.Platform, "/") {
		return nil, fmt.Errorf("resin platform must be single path segment")
	}
	u, err := url.Parse(urlText)
	if err != nil {
		return nil, fmt.Errorf("invalid resin url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("resin url scheme must be http/https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("resin url host is empty")
	}
	token := strings.TrimPrefix(u.EscapedPath(), "/")
	if token == "" {
		return nil, fmt.Errorf("resin url must include token path")
	}
	return &RuntimeConfig{
		ResinURL:    urlText,
		Platform:    cfg.Platform,
		ProxyOrigin: fmt.Sprintf("%s://%s", u.Scheme, u.Host),
		Token:       token,
	}, nil
}

func BuildAccount(apiKey string) string {
	sum := xxhash.Sum64String(apiKey)
	buf := make([]byte, 8)
	buf[0] = byte(sum >> 56)
	buf[1] = byte(sum >> 48)
	buf[2] = byte(sum >> 40)
	buf[3] = byte(sum >> 32)
	buf[4] = byte(sum >> 24)
	buf[5] = byte(sum >> 16)
	buf[6] = byte(sum >> 8)
	buf[7] = byte(sum)
	return "kilo:" + hex.EncodeToString(buf)
}

func BuildReverseURL(target string, cfg RuntimeConfig) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse target: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("target must be http/https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("target host required")
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	res := fmt.Sprintf("%s/%s/%s/%s%s", cfg.ResinURL, url.PathEscape(cfg.Platform), u.Scheme, u.Host, path)
	if u.RawQuery != "" {
		res += "?" + u.RawQuery
	}
	return res, nil
}
