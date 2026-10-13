package config

import (
	"errors"
	"net/url"
	"strings"
)

func ValidateOrigins(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return errors.New("CORS_ALLOWED_ORIGINS must contain explicit http(s) origins without wildcard or paths")
		}
	}
	return nil
}

func OriginAllowed(allowlist, origin string) bool {
	if ValidateOrigins(allowlist) != nil || origin == "" {
		return false
	}
	for _, allowed := range strings.Split(allowlist, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}
