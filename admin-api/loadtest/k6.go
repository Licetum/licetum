package loadtest

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// k6Script is the traffic: the operations of the mix, sent at the rate of the window (k6/check.js).
//
//go:embed k6/check.js
var k6Script []byte

// k6ThresholdExit is the exit code k6 gives when a threshold of the script is crossed. The script has none; the code is
// kept for a script that gets some.
const k6ThresholdExit = 99

// k6RawEnv keeps the points of every request of a window (k6 --out json), for reading the answers one by one.
const k6RawEnv = "LICETUM_LOADTEST_K6_RAW"

// k6Runner runs the traffic of the windows of a run with k6. The script and the tokens are written to the work
// directory, which is removed when the run ends: the tokens are credentials, and the report directory is kept.
type k6Runner struct {
	bin string
	// script is the path of check.js in the work directory.
	script string
	// env holds the settings that do not change between the windows.
	env []string
	// outDir is where the summary and the log of each window are written.
	outDir string
	// cpus is the CPU list k6 is pinned to (taskset), empty when it is not pinned.
	cpus string
	// issued is how many iterations the windows run so far have started. The next window numbers its iterations from
	// there (FIRST_ITERATION), so that the cold mode, which asks each question once, does not ask a window's questions again
	// in the next one.
	issued int
}

// newK6Runner writes the script and the tokens of the run to workDir.
func newK6Runner(bin, workDir, outDir, cpus string, tokens []string, backend string, p Profile, mix Mix, consistency string) (*k6Runner, error) {
	raw, err := json.Marshal(tokens)
	if err != nil {
		return nil, fmt.Errorf("encode the tokens: %w", err)
	}
	tokensPath := filepath.Join(workDir, "tokens.json")
	if err := os.WriteFile(tokensPath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write the tokens: %w", err)
	}
	script := filepath.Join(workDir, "check.js")
	if err := os.WriteFile(script, k6Script, 0o600); err != nil {
		return nil, fmt.Errorf("write the script: %w", err)
	}
	return &k6Runner{
		bin:    bin,
		script: script,
		outDir: outDir,
		cpus:   cpus,
		env: []string{
			"TOKENS=" + tokensPath,
			"BACKEND_TOKEN=" + backend,
			"SERVICE=" + serviceSlug,
			"USERS=" + strconv.Itoa(p.Users),
			"GROUPS=" + strconv.Itoa(p.Groups),
			"DOCUMENTS=" + strconv.Itoa(p.documents()),
			"RULES=" + strconv.Itoa(p.Rules),
			"FOLDERS=" + strconv.Itoa(p.folders()),
			"MIX=" + mix.String(),
			"CONSISTENCY=" + consistency,
		},
	}, nil
}

// window is one k6 invocation: the arrivals at the rate for the seconds, under a mode and a seed.
type window struct {
	name     string // the file name of the window's summary and log
	mode     string // warm, cold or mix
	seed     int64
	rate     int // arrivals per second
	seconds  int // length of the window
	preVUs   int
	maxVUs   int
	base     string   // the first check server
	bases    []string // every check server of the run (replicas); the base when empty
	rawEvery bool     // keep the points of each request
}

// k6Result is what one k6 invocation left behind: its summary, and the CPU its process used.
type k6Result struct {
	summary k6Summary
	elapsed time.Duration
	cpu     time.Duration
}

// run sends the traffic of one window and returns the summary k6 wrote. The CPU of k6 is read from its process when it
// exits, so it is the CPU of the generator alone (taskset does not add to it: it execs k6).
func (r *k6Runner) run(ctx context.Context, w window) (k6Result, error) {
	summary := filepath.Join(r.outDir, w.name+".summary.json")
	logPath := filepath.Join(r.outDir, w.name+".k6.log")
	logFile, err := os.Create(logPath) //nolint:gosec // a file in the report directory of this run
	if err != nil {
		return k6Result{}, fmt.Errorf("create the k6 log: %w", err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			slog.Warn("close the k6 log", "error", err)
		}
	}()

	args := []string{"run", "--quiet", "--no-color", "--summary-export", summary}
	if os.Getenv(k6RawEnv) != "" || w.rawEvery {
		// The points are large: k6 gzips the file when its name ends in .gz.
		args = append(args, "--out", "json="+filepath.Join(r.outDir, w.name+".points.json.gz"))
	}
	args = append(args, r.script)
	bin, args := r.command(args)

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the binary is the one the run was asked to use
	cmd.Env = append(append(os.Environ(), r.env...),
		"BASE_URL="+w.base,
		"BASES="+strings.Join(replicaBases(w), ","),
		"MODE="+w.mode,
		"SEED="+strconv.FormatInt(w.seed, 10),
		"FIRST_ITERATION="+strconv.Itoa(r.issued),
		"RATE="+strconv.Itoa(w.rate),
		"MEASURE_SECONDS="+strconv.Itoa(w.seconds),
		"WARM_SECONDS=0",
		"PRE_VUS="+strconv.Itoa(w.preVUs),
		"MAX_VUS="+strconv.Itoa(w.maxVUs),
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)
	_, _ = logFile.Write(out.Bytes())

	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != k6ThresholdExit) {
		return k6Result{}, fmt.Errorf("k6 %s: %w: %s", w.name, err, tail(out.Bytes()))
	}
	raw, err := os.ReadFile(summary) //nolint:gosec // the summary k6 wrote into the report directory of this run
	if err != nil {
		return k6Result{}, fmt.Errorf("k6 %s wrote no summary: %w", w.name, err)
	}
	var sum k6Summary
	if err := json.Unmarshal(raw, &sum); err != nil {
		return k6Result{}, fmt.Errorf("read the summary of %s: %w", w.name, err)
	}
	r.issued += sum.iterations()
	cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	return k6Result{summary: sum, elapsed: elapsed, cpu: cpu}, nil
}

// command is the program and its arguments: k6 itself, or taskset in front of it when the generator is pinned.
func (r *k6Runner) command(args []string) (string, []string) {
	if r.cpus == "" {
		return r.bin, args
	}
	return "taskset", append([]string{"-c", r.cpus, r.bin}, args...)
}

// k6Version is the first line of `k6 version`, for the environment.
func k6Version(bin string) string {
	out, err := exec.CommandContext(context.Background(), bin, "version").Output()
	if err != nil {
		return "unknown"
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

// k6Summary is the part of the summary k6 writes with --summary-export that the measurement reads: the statistics of each
// metric. A trend has avg, min, med, max and the percentiles; a counter has count and rate; a rate has value (the share
// of the true values, which is what http_req_failed records for the failed requests).
type k6Summary struct {
	// The statistics of a metric are its fields. Some fields are not numbers (the thresholds, the verdicts of the script),
	// so they stay raw and only the numbers are read.
	Metrics map[string]map[string]json.RawMessage `json:"metrics"`
}

// value is one statistic of a metric; a metric the run did not record, or a field that is not a number, is 0.
func (s k6Summary) value(metric, stat string) float64 {
	raw, ok := s.Metrics[metric][stat]
	if !ok {
		return 0
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0
	}
	return v
}

// iterations is how many iterations the window started (the arrivals that were dropped did not start).
func (s k6Summary) iterations() int {
	return int(s.value("iterations", "count") + 0.5)
}

// latency is the distribution of a trend, in milliseconds. A trend in the summary has no count of its own, so the
// samples are the requests of the operation (one trend sample per request).
func (s k6Summary) latency(metric string, samples int) Latency {
	return Latency{
		Samples: samples,
		Mean:    s.value(metric, "avg"),
		P50:     s.value(metric, "med"),
		P90:     s.value(metric, "p(90)"),
		P95:     s.value(metric, "p(95)"),
		P99:     s.value(metric, "p(99)"),
		P999:    s.value(metric, "p(99.9)"),
		Max:     s.value(metric, "max"),
	}
}

// tail is the end of a log, for an error message.
func tail(b []byte) string {
	const n = 800
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

// replicaBases is the list of check servers a window sends to: all the replicas, or the one base.
func replicaBases(w window) []string {
	if len(w.bases) == 0 {
		return []string{w.base}
	}
	return w.bases
}
