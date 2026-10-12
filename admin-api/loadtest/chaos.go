package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ChaosEvent is one fault the harness injected during a measured window, with the times it began and ended.
type ChaosEvent struct {
	Kind    string    `json:"kind"`
	Target  string    `json:"target"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
	Error   string    `json:"error,omitempty"`
}

// ChaosEffect is what an injected fault did to the requests of the window, from the raw points: the per-second p99 before
// the fault (the baseline), the highest per-second p99 during it, the answers that were not 2xx during it, and the time
// until a second's p99 is back within 1.5 times the baseline and stays there for three seconds.
type ChaosEffect struct {
	BaselineP99MS   float64 `json:"baseline_p99_ms"`
	PeakP99MS       float64 `json:"peak_p99_ms_during_fault"`
	ErrorsDuring    int     `json:"not_2xx_during_fault"`
	RequestsDuring  int     `json:"requests_during_fault"`
	RecoverySeconds float64 `json:"recovery_seconds"`
	RecoveredInRun  bool    `json:"recovered_before_the_window_ended"`
}

// injectRedisPause pauses the Redis container for the given time, starting at the given offset of the window, and
// records the event. The check servers lose their cache and their lock for that time: the answers come from the database.
func injectRedisPause(ctx context.Context, container string, at, length time.Duration) (ChaosEvent, error) {
	ev := ChaosEvent{Kind: "redis-pause", Target: container}
	select {
	case <-ctx.Done():
		ev.Error = ctx.Err().Error()
		return ev, ctx.Err()
	case <-time.After(at):
	}
	ev.Started = time.Now()
	if out, err := exec.CommandContext(ctx, "docker", "pause", container).CombinedOutput(); err != nil { //nolint:gosec // the container of the local test stack
		ev.Error = fmt.Sprintf("docker pause: %v: %s", err, out)
		return ev, fmt.Errorf("pause %s: %w", container, err)
	}
	slog.Info("paused the cache server", "container", container)
	select {
	case <-ctx.Done():
	case <-time.After(length):
	}
	if out, err := exec.CommandContext(context.Background(), "docker", "unpause", container).CombinedOutput(); err != nil { //nolint:gosec // the container of the local test stack
		ev.Error = fmt.Sprintf("docker unpause: %v: %s", err, out)
	}
	ev.Ended = time.Now()
	slog.Info("unpaused the cache server", "container", container)
	return ev, nil
}

// chaosEffect measures a fault against the requests of the window: the points are the raw k6 points, the event the
// interval of the fault.
func chaosEffect(points []point, ev ChaosEvent) ChaosEffect {
	var eff ChaosEffect
	if len(points) == 0 || ev.Started.IsZero() {
		return eff
	}
	start := points[0].t
	for _, p := range points {
		start = minTime(start, p.t)
	}
	perSecond := map[int][]float64{}
	for _, p := range points {
		s := int(p.t.Sub(start).Seconds())
		perSecond[s] = append(perSecond[s], p.dur)
		if !ev.Ended.IsZero() && !p.t.Before(ev.Started) && p.t.Before(ev.Ended) {
			eff.RequestsDuring++
			if p.status < 200 || p.status > 299 {
				eff.ErrorsDuring++
			}
		}
	}
	last := 0
	for s := range perSecond {
		last = max(last, s)
	}
	p99 := make([]float64, last+1)
	for s := range p99 {
		p99[s] = quantileOf(perSecond[s], 0.99)
	}
	pauseStart := int(ev.Started.Sub(start).Seconds())
	pauseEnd := int(ev.Ended.Sub(start).Seconds())
	var before []float64
	for s := max(0, pauseStart-20); s < pauseStart && s < len(p99); s++ {
		before = append(before, p99[s])
	}
	if len(before) == 0 {
		return eff
	}
	eff.BaselineP99MS = quantileOf(before, 0.5)
	for s := pauseStart; s <= pauseEnd && s < len(p99); s++ {
		eff.PeakP99MS = max(eff.PeakP99MS, p99[s])
	}
	limit := 1.5 * eff.BaselineP99MS
	for s := pauseEnd; s < len(p99); s++ {
		if isStable(p99, s, limit) {
			eff.RecoverySeconds = float64(s - pauseEnd)
			eff.RecoveredInRun = true
			break
		}
	}
	return eff
}

// writeChaos saves the fault events of a window next to its time series.
func writeChaos(outDir, window string, events []ChaosEvent) error {
	raw, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, window+".chaos.json"), append(raw, '\n'), 0o600)
}
