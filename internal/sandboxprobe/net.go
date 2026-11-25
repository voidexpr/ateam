package sandboxprobe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
)

// proxyVars are the variables a sandbox sets to route a command through
// its filtering proxy.
var proxyVars = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}

// ProxyVarsSet returns the proxy variables that are set, upper-case names
// once each.
func ProxyVarsSet() []string {
	var set []string
	for _, name := range proxyVars {
		upper := strings.ToUpper(name)
		if os.Getenv(name) != "" && !slices.Contains(set, upper) {
			set = append(set, upper)
		}
	}
	return set
}

// CheckNet tries one network operation and undoes it at once: a listener
// is closed, a connection sends nothing beyond a handshake or a HEAD.
func CheckNet(method Method, target string) (Permission, string) {
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	switch method {
	case MethodListen:
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", target)
		if err != nil {
			return Blocked, reason(err)
		}
		_ = ln.Close()
		return Listen, ""
	case MethodLoopback:
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return Blocked, "cannot listen"
		}
		defer ln.Close()
		return dial(ctx, ln.Addr().String())
	case MethodDial:
		return dial(ctx, target)
	case MethodDNS:
		if _, err := net.DefaultResolver.LookupHost(ctx, target); err != nil {
			return Blocked, reason(err)
		}
		return Connect, "resolves"
	case MethodURLProxy:
		proxy := proxyFor(target)
		if proxy == nil {
			perm, _ := head(ctx, target, nil)
			return perm, "no proxy variable is set: direct"
		}
		return head(ctx, target, proxy)
	case MethodURLDirect:
		return head(ctx, target, nil)
	}
	return Blocked, "unknown method " + string(method)
}

// reason says in a few words why a connection failed: no answer and a
// refusal are different findings.
func reason(err error) string {
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err):
		return "no answer in " + netTimeout.String()
	case errors.As(err, &dns):
		return "the name does not resolve"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES):
		return "not permitted"
	case errors.As(err, &op) && op.Err != nil:
		return op.Op + ": " + op.Err.Error()
	}
	text := err.Error()
	if at := strings.LastIndex(text, ": "); at >= 0 {
		text = text[at+2:]
	}
	return text
}

func dial(ctx context.Context, addr string) (Permission, string) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return Blocked, reason(err)
	}
	_ = conn.Close()
	return Connect, ""
}

// proxyFor returns the proxy the environment names for target, nil
// without one. ALL_PROXY is honoured: sandboxes set it, net/http does not
// read it.
func proxyFor(target string) *url.URL {
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	if proxy, err := http.ProxyFromEnvironment(&http.Request{URL: u}); err == nil && proxy != nil {
		return proxy
	}
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		if proxy, err := url.Parse(os.Getenv(name)); err == nil && proxy.Host != "" {
			return proxy
		}
	}
	return nil
}

// untrusted is said of an answer whose certificate this process does not
// trust: usually a proxy that opens TLS to filter what goes through.
const untrusted = "certificate not trusted here: a proxy that intercepts TLS?"

// head sends one HEAD request, through proxy when given, and follows no
// redirect. Any answer is a connection, except a filtering proxy's own
// refusal. The client is built here: no cookie, no stored credential.
//
// The question is whether something answers, not who: when the
// certificate is not trusted the request is sent once more without
// checking it, and the detail says so. A proxy that intercepts TLS accepts
// the tunnel of a host it refuses and answers inside it: stopping at the
// certificate would hide which of the two happened.
func head(ctx context.Context, target string, proxy *url.URL) (Permission, string) {
	status, err := headStatus(ctx, target, proxy, false)
	detail := ""
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		detail = untrusted
		status, err = headStatus(ctx, target, proxy, true)
	}
	if err != nil {
		return Blocked, joinDetail(reason(err), detail)
	}
	if proxy != nil && (status == http.StatusForbidden || status == http.StatusProxyAuthRequired || status == http.StatusBadGateway) {
		return Blocked, joinDetail(fmt.Sprintf("the proxy answered %d", status), detail)
	}
	return Connect, detail
}

func headStatus(ctx context.Context, target string, proxy *url.URL, skipVerify bool) (int, error) {
	transport := &http.Transport{DisableKeepAlives: true}
	if skipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // nothing is sent or read beyond a HEAD and its status
	}
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
