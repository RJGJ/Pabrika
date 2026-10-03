// Package config parses and validates the environment configuration.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Config is the parsed process configuration.
type Config struct {
	Port         int    // PORT, default 8080, 1..65535
	DBPath       string // DB_PATH, default ./data/pabrika.db
	BaseURL      string // BASE_URL, absolute http(s) URL without path, query or fragment; trailing slash trimmed
	AllowSignup  bool   // ALLOW_SIGNUP, default true
	CookieSecure bool   // COOKIE_SECURE, default true
	TrustProxy   bool   // TRUST_PROXY, default false
}

// Load reads the configuration through getenv (os.Getenv in production).
// An empty value means "use the default". Errors name the offending variable.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Port:         8080,
		DBPath:       "./data/pabrika.db",
		BaseURL:      "http://localhost:8080",
		AllowSignup:  true,
		CookieSecure: true,
		TrustProxy:   false,
	}

	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || p < 1 || p > 65535 {
			return Config{}, fmt.Errorf("PORT: %q is not a port number between 1 and 65535", v)
		}
		c.Port = p
	}
	if v := getenv("DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := getenv("BASE_URL"); v != "" {
		b, err := parseBaseURL(v)
		if err != nil {
			return Config{}, fmt.Errorf("BASE_URL: %w", err)
		}
		c.BaseURL = b
	}
	for _, b := range []struct {
		name string
		dst  *bool
	}{
		{"ALLOW_SIGNUP", &c.AllowSignup},
		{"COOKIE_SECURE", &c.CookieSecure},
		{"TRUST_PROXY", &c.TrustProxy},
	} {
		if v := getenv(b.name); v != "" {
			parsed, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return Config{}, fmt.Errorf("%s: %q is not a boolean", b.name, v)
			}
			*b.dst = parsed
		}
	}
	return c, nil
}

func parseBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("%q is not a valid URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%q must start with http:// or https://", raw)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("%q must not contain credentials", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%q must not have a path", raw)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("%q must not have a query or fragment", raw)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

// Origin returns BaseURL normalised the way a browser sends the Origin header:
// lowercase scheme and host, default port (80 for http, 443 for https) dropped.
func (c Config) Origin() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return strings.ToLower(c.BaseURL)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") { // IPv6 literal
		host = "[" + host + "]"
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}
