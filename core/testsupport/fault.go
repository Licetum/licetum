package support

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	toxiclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// Link is a Toxiproxy proxy in front of a TCP service (PostgreSQL): the code under test connects to the proxy,
// and the test (or the load test) makes the link slow, cuts it, or swallows the traffic. The Toxiproxy server runs
// inside the process (the same server as the toxiproxy-server binary, driven through its HTTP API), so no image is
// needed. Tests use FaultProxy, which fails the test on an error.
type Link struct {
	server *toxiproxy.ApiServer
	proxy  *toxiclient.Proxy
	listen string
}

// StartLink starts a proxy to the upstream address (host:port) on a free local port. Close it when done.
func StartLink(name, upstream string) (*Link, error) {
	apiAddr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), zerolog.Nop())
	go func() { _ = server.Listen(apiAddr) }()
	if err := waitListening(apiAddr); err != nil {
		return nil, err
	}
	listen, err := freeAddr()
	if err != nil {
		_ = server.Shutdown()
		return nil, err
	}
	proxy, err := toxiclient.NewClient(apiAddr).CreateProxy(name, listen, upstream)
	if err != nil {
		_ = server.Shutdown()
		return nil, fmt.Errorf("create the proxy %s: %w", name, err)
	}
	return &Link{server: server, proxy: proxy, listen: listen}, nil
}

// Close stops the proxy and its server.
func (l *Link) Close() {
	_ = l.proxy.Delete()
	_ = l.server.Shutdown()
}

// Addr is the address of the proxy (host:port): connect here instead of the upstream.
func (l *Link) Addr() string { return l.listen }

// Rewrite returns uri (postgres://…) with the host and the port of the proxy in place of its own.
func (l *Link) Rewrite(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", uri, err)
	}
	u.Host = l.listen
	return u.String(), nil
}

// SetLatency adds delay (± jitter) to every chunk of data, in each direction: a round trip takes twice as long.
func (l *Link) SetLatency(delay, jitter time.Duration) error {
	if err := l.Reset(); err != nil {
		return err
	}
	return l.addToxics("latency", "latency", toxiclient.Attributes{"latency": delay.Milliseconds(), "jitter": jitter.Milliseconds()})
}

// Cut closes the link: the open connections drop and new ones are refused, until Reset.
func (l *Link) Cut() error { return l.proxy.Disable() }

// BlackHole swallows the traffic and keeps the connections open: a call neither fails nor answers, until its own
// timeout, and a new connection is accepted but gets no reply. It lasts until Reset.
func (l *Link) BlackHole() error {
	if err := l.Reset(); err != nil {
		return err
	}
	return l.addToxics("blackhole", "timeout", toxiclient.Attributes{"timeout": 0})
}

// Reset removes every toxic and brings the link back.
func (l *Link) Reset() error {
	if err := l.proxy.Enable(); err != nil {
		return err
	}
	toxics, err := l.proxy.Toxics()
	if err != nil {
		return err
	}
	for _, toxic := range toxics {
		if err := l.proxy.RemoveToxic(toxic.Name); err != nil {
			return err
		}
	}
	return nil
}

func (l *Link) addToxics(name, kind string, attrs toxiclient.Attributes) error {
	for _, stream := range []string{"downstream", "upstream"} {
		if _, err := l.proxy.AddToxic(name+"-"+stream, kind, stream, 1, attrs); err != nil {
			return fmt.Errorf("add the toxic %s: %w", name, err)
		}
	}
	return nil
}

// FaultProxy is a Link for a test: it stops with the test, and a failure fails the test.
type FaultProxy struct {
	t    testing.TB
	link *Link
}

// NewFaultProxy starts a proxy to the upstream address (host:port).
func NewFaultProxy(t testing.TB, name, upstream string) *FaultProxy {
	t.Helper()
	link, err := StartLink(name, upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.Close)
	return &FaultProxy{t: t, link: link}
}

// Addr is the address of the proxy (host:port): connect here instead of the upstream.
func (f *FaultProxy) Addr() string { return f.link.Addr() }

// Rewrite returns uri with the host and the port of the proxy in place of its own.
func (f *FaultProxy) Rewrite(uri string) string {
	f.t.Helper()
	out, err := f.link.Rewrite(uri)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// Latency adds delay (± jitter) to every chunk of data, in each direction.
func (f *FaultProxy) Latency(delay, jitter time.Duration) {
	f.t.Helper()
	f.check(f.link.SetLatency(delay, jitter))
}

// Cut closes the link until Reset.
func (f *FaultProxy) Cut() { f.t.Helper(); f.check(f.link.Cut()) }

// BlackHole swallows the traffic and keeps the connections open until Reset.
func (f *FaultProxy) BlackHole() { f.t.Helper(); f.check(f.link.BlackHole()) }

// Reset removes every toxic and brings the link back.
func (f *FaultProxy) Reset() { f.t.Helper(); f.check(f.link.Reset()) }

func (f *FaultProxy) check(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func freeAddr() (string, error) {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func waitListening(addr string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp4", addr); err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("nothing listens on %s", addr)
}
