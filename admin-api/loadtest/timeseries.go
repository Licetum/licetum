package loadtest

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"
)

// sample is one reading of the server, as one line of timeseries.jsonl.
type sample struct {
	Time       time.Time          `json:"time"`
	CPUSeconds float64            `json:"check_cpu_seconds"`
	Series     map[string]float64 `json:"licetum_series"`
	DB         map[string]float64 `json:"database"`
}

// startSampler reads the server every interval until the returned stop function is called, and writes one line per
// reading to path. A reading that fails is logged and skipped: the measurement goes on without it.
func startSampler(ctx context.Context, obs observer, interval time.Duration, path string) (func(), error) {
	file, err := os.Create(path) //nolint:gosec // a file in the report directory of this run
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(file)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			reading, err := obs.read(ctx)
			if err != nil {
				slog.Warn("read the server", "error", err)
				continue
			}
			line := sample{Time: reading.at, CPUSeconds: reading.cpu, Series: licetumSeries(reading.series), DB: reading.db}
			if err := enc.Encode(line); err != nil {
				slog.Warn("write the time series", "error", err)
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		if err := file.Close(); err != nil {
			slog.Warn("close the time series", "error", err)
		}
	}, nil
}

// licetumSeries keeps the series of the check server, without the histogram buckets (their sums and counts are kept).
func licetumSeries(all map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for key, value := range all {
		if strings.HasPrefix(key, "licetum_") && !strings.Contains(key, "_bucket") {
			out[key] = value
		}
	}
	return out
}
