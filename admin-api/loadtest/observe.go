package loadtest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// clockTicks is the number of clock ticks per second in /proc/<pid>/stat (USER_HZ, 100 on Linux).
const clockTicks = 100

// observer reads what a run can see: the CPU time and the metrics of the check server, and the counters of the
// database. Its statements are measured only when the server preloads pg_stat_statements (see enableStatements).
type observer struct {
	pool       *pgxpool.Pool
	checks     []*checkServer // the replicas of the check server: their CPU and metrics are summed
	statements bool
}

// counters is one reading. A step is the difference of two readings.
type counters struct {
	at     time.Time
	cpu    float64            // seconds of user and system CPU of the check process
	series map[string]float64 // /metrics, keyed by the series as printed: name and labels
	db     map[string]float64 // database counters, and the statements ones when measured
	swap   float64            // pages the host swapped in and out since boot (see swapPages)
}

func (o observer) read(ctx context.Context) (counters, error) {
	var cpu float64
	series := map[string]float64{}
	for _, c := range o.checks {
		used, err := procCPU(c.cmd.Process.Pid)
		if err != nil {
			return counters{}, err
		}
		cpu += used
		one, err := scrape(ctx, c.base)
		if err != nil {
			return counters{}, err
		}
		for key, value := range one {
			series[key] += value
		}
	}
	db, err := databaseCounters(ctx, o.pool, o.statements)
	if err != nil {
		return counters{}, err
	}
	swap, err := swapPages()
	if err != nil {
		return counters{}, err
	}
	return counters{at: time.Now(), cpu: cpu, series: series, db: db, swap: swap}, nil
}

// swapPages is the number of pages the host has swapped in and out since boot. A window during which it grows ran with
// the host short of memory: its latencies belong to the host, not to the server, so it is not valid.
func swapPages() (float64, error) {
	raw, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		return 0, fmt.Errorf("read the swap counters: %w", err)
	}
	return parseSwapPages(string(raw)), nil
}

// parseSwapPages adds the pswpin and pswpout lines of /proc/vmstat.
func parseSwapPages(vmstat string) float64 {
	var total float64
	for line := range strings.SplitSeq(vmstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "pswpin" && fields[0] != "pswpout") {
			continue
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err == nil {
			total += v
		}
	}
	return total
}

// enableStatements turns on the statistics of the statements the database runs. It works only when the server preloads
// the extension (shared_preload_libraries); without it the run still measures the server, and the database is a black
// box in the report.
func enableStatements(ctx context.Context, o observer) observer {
	if _, err := o.pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		slog.Warn("statement statistics are off", "error", err)
		return o
	}
	if _, err := o.pool.Exec(ctx, `SELECT 1 FROM pg_stat_statements LIMIT 1`); err != nil {
		slog.Warn("statement statistics are off: start the server with shared_preload_libraries=pg_stat_statements", "error", err)
		return o
	}
	o.statements = true
	return o
}

// procCPU is the user and system CPU time the process has used, in seconds (Linux only: the run is for CI and servers).
func procCPU(pid int) (float64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("read the CPU time of the check server: %w", err)
	}
	// The command name is in parentheses and may hold spaces: the fields start after the last ')'. The first of them
	// is field 3 (the state), so utime (field 14) and stime (field 15) are the 12th and 13th.
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return 0, fmt.Errorf("unexpected /proc stat format")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 13 {
		return 0, fmt.Errorf("unexpected /proc stat format: %d fields", len(fields))
	}
	utime, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return 0, fmt.Errorf("utime: %w", err)
	}
	stime, err := strconv.ParseFloat(fields[12], 64)
	if err != nil {
		return 0, fmt.Errorf("stime: %w", err)
	}
	return (utime + stime) / clockTicks, nil
}

// scrape reads /metrics of the check server.
func scrape(ctx context.Context, base string) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape /metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape /metrics: status %d", resp.StatusCode)
	}
	return parseExposition(resp.Body)
}

// parseExposition reads the Prometheus text format: every sample line is "series value", and the series is kept as
// printed, so it can be looked up by name and labels. Comments and malformed lines are skipped.
func parseExposition(r io.Reader) (map[string]float64, error) {
	out := map[string]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			continue
		}
		out[line[:i]] = v
	}
	return out, sc.Err()
}

// databaseCounters reads the counters of the run's database: commits, blocks read from disk and found in the buffer
// cache, and, when measured, the time and the calls of its statements. The statistics of PostgreSQL are flushed with a
// delay, so a step's figures are close, not exact.
func databaseCounters(ctx context.Context, pool *pgxpool.Pool, statements bool) (map[string]float64, error) {
	var commit, read, hit, returned int64
	err := pool.QueryRow(ctx, `
		SELECT xact_commit, blks_read, blks_hit, tup_returned
		FROM pg_stat_database WHERE datname = current_database()`).Scan(&commit, &read, &hit, &returned)
	if err != nil {
		return nil, fmt.Errorf("read the database counters: %w", err)
	}
	out := map[string]float64{
		"xact_commit":  float64(commit),
		"blks_read":    float64(read),
		"blks_hit":     float64(hit),
		"tup_returned": float64(returned),
	}
	if !statements {
		return out, nil
	}
	var execMS, calls float64
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_exec_time), 0), COALESCE(SUM(calls), 0)
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&execMS, &calls)
	if err != nil {
		return nil, fmt.Errorf("read the statement counters: %w", err)
	}
	out["exec_ms"] = execMS
	out["calls"] = calls
	return out, nil
}

func delta(before, after map[string]float64, key string) float64 {
	return after[key] - before[key]
}
