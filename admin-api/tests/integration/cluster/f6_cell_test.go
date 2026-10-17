//go:build integration && linux

package cluster_test

// F6-10: a cell has its own PostgreSQL and Redis and no network path to another cell. The network policy of the chart enforces that
// in the cluster (deploy/helm/test.sh checks what it allows); this test checks the processes themselves: while a cell works (checks,
// writes, synchronizations) the sockets of the process go only to the endpoints of the cell, and nothing connects to the
// addresses of a "neighbouring cell" that are listening here to catch it.

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	support "github.com/licetum/licetum/core/testsupport"
)

// decoy listens where a neighbouring cell's PostgreSQL or Redis would, and counts who knocks.
type decoy struct {
	ln       net.Listener
	attempts atomic.Int64
}

func newDecoy(t *testing.T) *decoy {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &decoy{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.attempts.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return d
}

func (d *decoy) port() int { return d.ln.Addr().(*net.TCPAddr).Port }

func portOf(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("no port in %q", raw)
	}
	return port
}

// remotePorts are the remote ports of the connections this process opened (the client side of its TCP sockets right now): a socket
// whose local port is one this process listens on is the server end of a connection somebody opened to it, and is left out.
func remotePorts() (map[int]bool, error) {
	inodes := map[string]bool{}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil, err
	}
	for _, fd := range fds {
		link, err := os.Readlink("/proc/self/fd/" + fd.Name())
		if err == nil && strings.HasPrefix(link, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	type socket struct {
		local, remote int
		listen        bool
	}
	var sockets []socket
	port := func(addr string) int {
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			return 0
		}
		n, err := strconv.ParseUint(addr[i+1:], 16, 16)
		if err != nil {
			return 0
		}
		return int(n)
	}
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(f)
		scan.Scan() // header
		for scan.Scan() {
			fields := strings.Fields(scan.Text())
			if len(fields) < 10 || !inodes[fields[9]] {
				continue
			}
			sockets = append(sockets, socket{local: port(fields[1]), remote: port(fields[2]), listen: fields[3] == "0A"})
		}
		_ = f.Close()
	}
	listening := map[int]bool{}
	for _, s := range sockets {
		if s.listen {
			listening[s.local] = true
		}
	}
	out := map[int]bool{}
	for _, s := range sockets {
		if !s.listen && !listening[s.local] && s.remote != 0 {
			out[s.remote] = true
		}
	}
	return out, nil
}

func TestF6_10_ACellOpensNoConnectionOutsideItself(t *testing.T) {
	// What the test binary held before the cell started (the connection of Testcontainers to its reaper, for one) is not the cell's.
	baseline, err := remotePorts()
	if err != nil {
		t.Fatal(err)
	}
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org", 0)
	otherPG, otherRedis := newDecoy(t), newDecoy(t)

	// What the cell may talk to: its PostgreSQL and Redis, the identity provider, the source, and its own servers.
	allowed := map[int]bool{
		portOf(t, infra.DBURI): true, portOf(t, infra.RedisURI): true,
		portOf(t, c.IdP.URL()): true, portOf(t, c.Source.URL()): true, portOf(t, c.AdminServer.URL): true,
	}
	for _, r := range c.Replicas {
		allowed[portOf(t, r.Server.URL)] = true
	}
	// The suites through PgBouncer reach the cell's PostgreSQL by way of the pooler.
	if addr := os.Getenv(support.EnvPgBouncerAddr); addr != "" {
		allowed[portOf(t, "tcp://"+addr)] = true
	}
	for _, port := range []int{otherPG.port(), otherRedis.port()} {
		if allowed[port] {
			t.Fatalf("port %d is both allowed and a decoy", port)
		}
	}

	seen := map[int]bool{}
	var mu sync.Mutex
	stop := make(chan struct{})
	var scans sync.WaitGroup
	scans.Add(1)
	go func() {
		defer scans.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			if ports, err := remotePorts(); err == nil {
				mu.Lock()
				for port := range ports {
					seen[port] = true
				}
				mu.Unlock()
			}
		}
	}()

	// The cell works: checks on both replicas, writes through the admin API, a synchronization of a new type.
	deadline := time.Now().Add(4 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		user := fmt.Sprintf("cell-user-%d", i%5)
		for _, r := range c.Replicas {
			r.Permission(c, p.Slug, c.UserToken(user))
			r.Do(c, http.MethodPost, "/service/"+p.Slug+"/check", c.UserToken(user),
				map[string]any{"checks": []map[string]any{{"type": "branch", "resource_id": "b1", "action": "read"}}})
		}
		if i%3 == 0 {
			c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, nil))
		}
	}
	close(stop)
	scans.Wait()

	mu.Lock()
	defer mu.Unlock()
	var outside []int
	for port := range seen {
		if !allowed[port] && !baseline[port] {
			outside = append(outside, port)
		}
	}
	t.Logf("the process held connections to %d remote ports, %d outside the cell", len(seen), len(outside))
	if len(outside) > 0 {
		t.Errorf("connections to ports outside the cell: %v", outside)
	}
	if n := otherPG.attempts.Load() + otherRedis.attempts.Load(); n != 0 {
		t.Errorf("%d connections to the neighbouring cell", n)
	}
}
