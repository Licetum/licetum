package loadtest

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// checkServer is the check API under test: the licetum binary run as its own process, so the CPU the server spends is
// the CPU of that process alone, not of the load generator.
type checkServer struct {
	cmd  *exec.Cmd
	base string // the public port: the API under test
	ops  string // the ops listener (PDP__OPS_ADDR): the probes and the metrics
	log  *os.File
	done chan struct{} // closed when the process has exited
}

// waitReady polls the health endpoint of the ops listener until the server answers, the process exits, or the time is up. The
// ops listener is bound first, so the public port, which the load runs against, is awaited too.
func (c *checkServer) waitReady(ctx context.Context, timeout time.Duration) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ops+"/healthz", nil)
		if err != nil {
			return err
		}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && c.publicAnswers(ctx, client) {
				return nil
			}
		}
		select {
		case <-c.done:
			return fmt.Errorf("the check server exited during start-up (see check.log)")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the check server did not answer on %s within %v (see check.log)", c.ops, timeout)
		}
	}
}

// publicAnswers reports whether the public port accepts connections: any answer counts, the root path is a JSON 404.
func (c *checkServer) publicAnswers(ctx context.Context, client *http.Client) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// stop asks the server to shut down, and kills it when it does not do so in time.
func (c *checkServer) stop() {
	_ = c.cmd.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
	case <-time.After(15 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	if err := c.log.Close(); err != nil {
		slog.Warn("close the check server log", "error", err)
	}
}

// freeAddr is a loopback address nothing listens on yet.
func freeAddr() (string, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("find a free port: %w", err)
	}
	addr := ln.Addr().String()
	return addr, ln.Close()
}
