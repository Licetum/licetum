package loadtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// checkServer is the check API under test: the licetum binary run as its own process, so the CPU the server spends is
// the CPU of that process alone, not of the load generator.
type checkServer struct {
	cmd  *exec.Cmd
	base string
	log  *os.File
	done chan struct{} // closed when the process has exited
}

// startCheck runs `licetum check` against the infrastructure of the run. Its pool is the one the options name, with no
// overflow, so the number of connections to PostgreSQL is the setting under test. Its output goes to check.log.
func startCheck(ctx context.Context, e *env, opts Options) (*checkServer, error) {
	addr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	logFile, err := os.Create(filepath.Join(opts.OutDir, "check.log"))
	if err != nil {
		return nil, fmt.Errorf("create the server log: %w", err)
	}
	cmd := exec.CommandContext(ctx, opts.CheckBin, "check") //nolint:gosec // the binary is the one the run was asked to measure
	cmd.Env = append(os.Environ(),
		"DB__URI="+e.dbURI,
		"DB__POOL_SIZE="+strconv.Itoa(opts.DBPool),
		"DB__MAX_OVERFLOW=0",
		"REDIS__URI="+e.redisURI,
		"AUTH__STATE_SECRET="+e.stateSecret,
		"HTTP__ADDR="+addr,
	)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s: %w", opts.CheckBin, err)
	}
	cs := &checkServer{cmd: cmd, base: "http://" + addr, log: logFile, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(cs.done)
	}()
	if err := cs.waitReady(ctx, time.Minute); err != nil {
		cs.stop()
		return nil, err
	}
	return cs, nil
}

// waitReady polls the health endpoint until the server answers, the process exits, or the time is up.
func (c *checkServer) waitReady(ctx context.Context, timeout time.Duration) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
		if err != nil {
			return err
		}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
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
			return fmt.Errorf("the check server did not answer on %s within %v (see check.log)", c.base, timeout)
		}
	}
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
	_ = c.log.Close()
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
