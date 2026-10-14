package support

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	toxiclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// FaultProxy is a Toxiproxy proxy in front of a TCP service (PostgreSQL or Redis): the code under test connects to
// the proxy, and the test makes the link slow, cuts it, or swallows the traffic. The Toxiproxy server runs inside the
// test process (the same server as the toxiproxy-server binary, driven through its HTTP API), so no image is needed.
type FaultProxy struct {
	t      testing.TB
	server *toxiproxy.ApiServer
	proxy  *toxiclient.Proxy
	listen string
}

// NewFaultProxy starts a proxy to the upstream address (host:port).
func NewFaultProxy(t testing.TB, name, upstream string) *FaultProxy {
	t.Helper()
	apiAddr := freeAddr(t)
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), zerolog.Nop())
	go func() { _ = server.Listen(apiAddr) }()
	waitListening(t, apiAddr)
	t.Cleanup(func() { _ = server.Shutdown() })

	client := toxiclient.NewClient(apiAddr)
	listen := freeAddr(t)
	proxy, err := client.CreateProxy(name, listen, upstream)
	if err != nil {
		t.Fatalf("create the proxy %s: %v", name, err)
	}
	t.Cleanup(func() { _ = proxy.Delete() })
	return &FaultProxy{t: t, server: server, proxy: proxy, listen: listen}
}

// Addr is the address of the proxy (host:port): connect here instead of the upstream.
func (f *FaultProxy) Addr() string { return f.listen }

// Rewrite returns uri (postgres://… or redis://…) with the host and the port of the proxy in place of its own.
func (f *FaultProxy) Rewrite(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		f.t.Fatalf("parse %q: %v", uri, err)
	}
	u.Host = f.listen
	return u.String()
}

// Latency adds delay (± jitter) to every chunk of data, in both directions.
func (f *FaultProxy) Latency(delay, jitter time.Duration) {
	f.t.Helper()
	f.Reset()
	for _, stream := range []string{"downstream", "upstream"} {
		f.addToxic("latency-"+stream, "latency", stream, toxiclient.Attributes{
			"latency": delay.Milliseconds(), "jitter": jitter.Milliseconds(),
		})
	}
}

// Cut closes the link: the open connections drop and new ones are refused, until Reset.
func (f *FaultProxy) Cut() {
	f.t.Helper()
	if err := f.proxy.Disable(); err != nil {
		f.t.Fatalf("cut the proxy: %v", err)
	}
}

// BlackHole swallows the traffic and keeps the connections open: a call neither fails nor answers, until its own
// timeout, and a new connection is accepted but gets no reply. It lasts until Reset.
func (f *FaultProxy) BlackHole() {
	f.t.Helper()
	f.Reset()
	for _, stream := range []string{"downstream", "upstream"} {
		f.addToxic("blackhole-"+stream, "timeout", stream, toxiclient.Attributes{"timeout": 0})
	}
}

// Reset removes every toxic and brings the link back.
func (f *FaultProxy) Reset() {
	f.t.Helper()
	if err := f.proxy.Enable(); err != nil {
		f.t.Fatalf("enable the proxy: %v", err)
	}
	toxics, err := f.proxy.Toxics()
	if err != nil {
		f.t.Fatalf("list the toxics: %v", err)
	}
	for _, toxic := range toxics {
		if err := f.proxy.RemoveToxic(toxic.Name); err != nil {
			f.t.Fatalf("remove the toxic %s: %v", toxic.Name, err)
		}
	}
}

func (f *FaultProxy) addToxic(name, kind, stream string, attrs toxiclient.Attributes) {
	f.t.Helper()
	if _, err := f.proxy.AddToxic(name, kind, stream, 1, attrs); err != nil {
		f.t.Fatalf("add the toxic %s: %v", name, err)
	}
}

func freeAddr(t testing.TB) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitListening(t testing.TB, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp4", addr); err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing listens on %s", addr)
}
