package sandboxprobe

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// UserChecks returns the checks asked for on the command line: paths,
// HOST[:PORT] destinations, URLs and [ADDR:]PORT listeners. They are
// reported, not judged, unless a policy covers them.
func UserChecks(env Env, paths, connects, urls, listens []string) ([]Check, error) {
	var checks []Check
	for _, path := range paths {
		switch {
		case path == "~":
			path = env.Home
		case strings.HasPrefix(path, "~/"):
			path = env.home(path[2:])
		case !filepath.IsAbs(path):
			path = env.work(path)
		}
		checks = append(checks, env.file("paths", filepath.Clean(path), "", nil))
	}
	network := func(name string, method Method, target string) {
		checks = append(checks, Check{ID: "network:" + name, Group: "network", Type: TypeNetwork, Method: method, Target: target, Name: name})
	}
	for _, dest := range connects {
		if _, _, err := net.SplitHostPort(dest); err != nil {
			dest = net.JoinHostPort(dest, "443")
		}
		network(dest+" (direct)", MethodDial, dest)
	}
	for _, raw := range urls {
		if u, err := url.Parse(raw); err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("--url %q: want an http or https URL", raw)
		}
		checks = append(checks, URLCheck("network", raw, MethodURLProxy), URLCheck("network", raw, MethodURLDirect))
	}
	for _, addr := range listens {
		if _, err := strconv.Atoi(addr); err == nil {
			addr = "127.0.0.1:" + addr
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return nil, fmt.Errorf("--listen-check %q: want PORT or ADDR:PORT", addr)
		}
		network(addr+" (listen)", MethodListen, addr)
	}
	return checks, nil
}
