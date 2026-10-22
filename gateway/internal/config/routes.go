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

var literalSegment = regexp.MustCompile(`^[a-zA-Z0-9_.~-]+$`)
var parameterSegment = regexp.MustCompile(`^\{([a-zA-Z][a-zA-Z0-9_]*)\}$`)

func Parameters(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return nil, fmt.Errorf("paths must be absolute and have no trailing slash")
	}
	names := []string{}
	seen := map[string]bool{}
	for _, segment := range strings.Split(path[1:], "/") {
		if match := parameterSegment.FindStringSubmatch(segment); match != nil {
			if seen[match[1]] {
				return nil, fmt.Errorf("duplicate path parameter")
			}
			seen[match[1]] = true
			names = append(names, match[1])
		} else if !literalSegment.MatchString(segment) || segment == "." || segment == ".." {
			return nil, fmt.Errorf("invalid path segment")
		}
	}
	return names, nil
}

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
	source, err := Parameters(route.Path)
	if err != nil {
		return err
	}
	targetNames, err := Parameters(route.TargetPath)
	if err != nil {
		return err
	}
	if len(source) != len(targetNames) {
		return fmt.Errorf("source and target must preserve path parameters")
	}
	for index := range source {
		if source[index] != targetNames[index] {
			return fmt.Errorf("source and target must preserve ordered path parameter names")
		}
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
