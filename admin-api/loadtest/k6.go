package loadtest

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// k6Script is the traffic of a step: the requests of the mix, sent at the rate of the step (k6/check.js).
//
//go:embed k6/check.js
var k6Script []byte

// k6ThresholdExit is the exit code k6 gives when a threshold of the script is crossed. The harness judges a step from
// the summary, so this is not a failure of the run.
const k6ThresholdExit = 99

// k6RawEnv keeps the points of every request of a run (k6 --out json), for reading the answers one by one.
const k6RawEnv = "LICETUM_LOADTEST_K6_RAW"

// k6Runner runs the steps of a run with k6. The script and the tokens are written to the work directory, which is
// removed when the run ends: the tokens are credentials, and the report directory is kept as an artifact.
type k6Runner struct {
	bin string
	// script is the path of check.js in the work directory.
	script string
	// env holds the settings that do not change between the steps.
	env []string
	// outDir is where the summary and the log of each step are written.
	outDir string
}

// newK6Runner writes the script and the tokens of the run to workDir.
func newK6Runner(bin, workDir, outDir string, tokens []string, backend string, p Profile, mix Mix, consistency string) (*k6Runner, error) {
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

// stepSettings are the settings of one step, on top of the run's.
type stepSettings struct {
	base        string        // the check server
	rate        int           // arrivals per second
	duration    time.Duration // how long the arrivals last
	preVUs      int           // virtual users started before the step
	maxVUs      int           // the most virtual users the step can use: when they are all busy, an arrival is dropped
	sloP99      time.Duration
	maxErrorRte float64
	name        string // the file name of the step's summary and log
}

// run sends the traffic of one step and returns the summary k6 wrote. A step that crosses a threshold is not an error
// here: its summary is returned, and the harness judges it.
func (r *k6Runner) run(ctx context.Context, s stepSettings) (k6Summary, time.Duration, error) {
	summary := filepath.Join(r.outDir, s.name+".summary.json")
	logPath := filepath.Join(r.outDir, s.name+".k6.log")
	logFile, err := os.Create(logPath) //nolint:gosec // a file in the report directory of this run
	if err != nil {
		return k6Summary{}, 0, fmt.Errorf("create the k6 log: %w", err)
	}
	defer logFile.Close()

	args := []string{"run", "--quiet", "--no-color", "--summary-export", summary}
	if os.Getenv(k6RawEnv) != "" {
		// The points of every request, with their times: for reading where the slow answers come from.
		args = append(args, "--out", "json="+filepath.Join(r.outDir, s.name+".points.json"))
	}
	cmd := exec.CommandContext(ctx, r.bin, append(args, r.script)...) //nolint:gosec // the binary is the one the run was asked to use
	cmd.Env = append(append(os.Environ(), r.env...),
		"BASE_URL="+s.base,
		"RATE="+strconv.Itoa(s.rate),
		"DURATION="+strconv.Itoa(int(s.duration.Seconds()))+"s",
		"PRE_VUS="+strconv.Itoa(s.preVUs),
		"MAX_VUS="+strconv.Itoa(s.maxVUs),
		"SLO_P99_MS="+strconv.FormatFloat(ms(s.sloP99), 'f', -1, 64),
		"MAX_ERROR_RATE="+strconv.FormatFloat(s.maxErrorRte, 'f', -1, 64),
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
		return k6Summary{}, elapsed, fmt.Errorf("k6 %s: %w: %s", s.name, err, tail(out.Bytes()))
	}
	raw, err := os.ReadFile(summary) //nolint:gosec // the summary k6 wrote into the report directory of this run
	if err != nil {
		return k6Summary{}, elapsed, fmt.Errorf("k6 %s wrote no summary: %w", s.name, err)
	}
	var sum k6Summary
	if err := json.Unmarshal(raw, &sum); err != nil {
		return k6Summary{}, elapsed, fmt.Errorf("read the summary of %s: %w", s.name, err)
	}
	return sum, elapsed, nil
}

// k6Version is the first line of `k6 version`, for the report.
func k6Version(bin string) string {
	out, err := exec.CommandContext(context.Background(), bin, "version").Output()
	if err != nil {
		return "unknown"
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

// k6Summary is the part of the summary k6 writes with --summary-export that the report reads: the values of each
// metric. A trend has avg, min, med, max, p(90), p(95) and p(99); a counter has count and rate; a rate has rate.
type k6Summary struct {
	// The statistics of a metric are its fields. Some fields are not numbers (the thresholds, which are the verdicts of
	// the script), so they stay raw and only the numbers are read.
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

// trend is the latency distribution of a metric, in milliseconds.
func (s k6Summary) trend(metric string) Latency {
	return Latency{
		P50:  s.value(metric, "med"),
		P90:  s.value(metric, "p(90)"),
		P95:  s.value(metric, "p(95)"),
		P99:  s.value(metric, "p(99)"),
		Max:  s.value(metric, "max"),
		Mean: s.value(metric, "avg"),
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
