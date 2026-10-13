package config

import (
	"net/url"
	"strings"
)

// GoogleRedirectAllowed matches the complete configured callback URI, not a
// caller-provided prefix. Local HTTP is available only outside production.
func (c *Config) GoogleRedirectAllowed() bool {
	u, err := url.Parse(c.GoogleRedirectURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "https" && !(c.AppEnv != "production" && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return false
	}
	for _, allowed := range strings.Split(c.GoogleRedirectAllowedURLs, ",") {
		if strings.TrimSpace(allowed) == c.GoogleRedirectURL {
			return true
		}
	}
	return false
}
