package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Routes are explicit allowlists: undeclared paths and methods are never proxied.
type Route struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	Upstream     string `json:"upstream"`
	TargetPath   string `json:"target_path"`
	AuthRequired bool   `json:"auth_required"`
	RateLimited  bool   `json:"rate_limited"`
}

var pathPattern = regexp.MustCompile(`^/[a-zA-Z0-9/_-]+$`)

func LoadRoutes(path string) ([]Route, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var routes []Route
	if err = decoder.Decode(&routes); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("expected one route array")
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("route configuration is empty")
	}
	seen := map[string]bool{}
	for index := range routes {
		route := &routes[index]
		route.Upstream = os.Expand(route.Upstream, os.Getenv)
		if err = Validate(*route); err != nil {
			return nil, fmt.Errorf("route %d: %w", index, err)
		}
		key := route.Method + " " + route.Path
		if seen[key] {
			return nil, fmt.Errorf("duplicate route %s", key)
		}
		seen[key] = true
	}
	return routes, nil
}

func Validate(route Route) error {
	if !pathPattern.MatchString(route.Path) || !pathPattern.MatchString(route.TargetPath) {
		return fmt.Errorf("route paths must be literal absolute paths")
	}
	if route.Path == "/healthz" || route.Path == "/readyz" {
		return fmt.Errorf("probe paths are reserved")
	}
	if !strings.Contains("|GET|POST|PUT|PATCH|DELETE|", "|"+route.Method+"|") {
		return fmt.Errorf("unsupported HTTP method")
	}
	target, err := url.Parse(route.Upstream)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return fmt.Errorf("upstream must be an HTTP(S) origin with no path, credentials or query")
	}
	return nil
}
