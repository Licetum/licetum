package engine

import (
	"time"

	"github.com/licetum/licetum/core/config"
)

// Defaults of the settings that a zero value in the configuration stands for (the Config built by hand in a test, or
// loaded from an environment that does not name them). config.Load sets the same values.
const (
	defaultTokenL1Size     = 50_000
	defaultAnswerL1Size    = 50_000
	defaultJWKSTTL         = 10 * time.Minute
	defaultJWKSMinRefresh  = 30 * time.Second
	defaultInvalidTokenTTL = 10 * time.Second
	defaultSnapshotMax     = 256 << 20
	defaultRevisionPoll    = 250 * time.Millisecond
	defaultRevisionWait    = 500 * time.Millisecond
	defaultSnapshotMaxAge  = time.Minute
	defaultWorkers         = 16
	defaultQueue           = 1024

	// invalidTokenCacheSize bounds the memory of the tokens remembered as invalid.
	invalidTokenCacheSize = 10_000
	// maxTokenL1TTL and maxAnswerL1TTL bound how long an in-memory entry lives, whatever the entry says.
	maxTokenL1TTL  = 60 * time.Second
	maxAnswerL1TTL = 10 * time.Second
)

// tuning is the effective value of the settings of the fast read path: a zero in the configuration took the default,
// a negative value turned the feature off (0 here).
type tuning struct {
	tokenL1Size, answerL1Size                  int
	jwksTTL, jwksMinRefresh, invalidTokenTTL   time.Duration
	snapshotMaxBytes                           int64
	revisionPoll, revisionWait, snapshotMaxAge time.Duration
	computeConcurrency                         int
	workers, queue                             int
}

func newTuning(cfg *config.Config) tuning {
	if cfg == nil {
		cfg = &config.Config{}
	}
	size := func(v, def int) int {
		switch {
		case v == 0:
			return def
		case v < 0:
			return 0
		}
		return v
	}
	span := func(v, def time.Duration) time.Duration {
		if v == 0 {
			return def
		}
		return v
	}
	t := tuning{
		tokenL1Size:     size(cfg.Auth.TokenL1Size, defaultTokenL1Size),
		answerL1Size:    size(cfg.Cache.AnswerL1Size, defaultAnswerL1Size),
		jwksTTL:         span(cfg.Auth.JWKSTTL, defaultJWKSTTL),
		jwksMinRefresh:  span(cfg.Auth.JWKSMinRefresh, defaultJWKSMinRefresh),
		invalidTokenTTL: span(cfg.Auth.InvalidTokenTTL, defaultInvalidTokenTTL),
		revisionPoll:    span(cfg.Revision.PollInterval, defaultRevisionPoll),
		revisionWait:    span(cfg.Revision.WaitMax, defaultRevisionWait),
		snapshotMaxAge:  span(cfg.Snapshot.MaxAge, defaultSnapshotMaxAge),
		workers:         size(cfg.Background.Workers, defaultWorkers),
		queue:           size(cfg.Background.Queue, defaultQueue),
	}
	switch v := cfg.Snapshot.MaxBytes; {
	case v == 0:
		t.snapshotMaxBytes = defaultSnapshotMax
	case v > 0:
		t.snapshotMaxBytes = v
	}
	t.computeConcurrency = cfg.Compute.Concurrency
	if t.computeConcurrency <= 0 {
		t.computeConcurrency = max(1, cfg.DB.PoolSize/2)
		if cfg.DB.PoolSize <= 0 {
			t.computeConcurrency = 5 // half of the default pool
		}
	}
	if t.workers == 0 {
		t.workers = 1
	}
	if cfg.Snapshot.MaxAge < 0 {
		t.snapshotMaxAge = 0
	}
	if cfg.Auth.InvalidTokenTTL < 0 {
		t.invalidTokenTTL = 0
	}
	return t
}

// tuningOrDefault is the settings of the App; an App built by hand (without New) has the defaults.
func (a *App) tuningOrDefault() tuning {
	if a.tune == (tuning{}) {
		return newTuning(a.Cfg)
	}
	return a.tune
}
