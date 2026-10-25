// Package service is the Licetum business logic. The HTTP layer (internal/api) calls App methods and
// never touches the DB directly. Errors meant for the client are *apperr.Error; the HTTP layer
// returns anything else as a 500.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/pepauth"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/synchronizer"
)

type App struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
	// SyncDB is the pool of the synchronization transactions (DB__SYNC_POOL_SIZE connections), apart from the request pool,
	// so that a synchronization waiting for a lock never takes a connection a request needs. Nil: DB is used.
	SyncDB *pgxpool.Pool
	// Role is what this process does with the synchronization (see Role). Only a worker applies it.
	Role Role
	// Hooks are for the tests that stop an executor at a chosen point.
	Hooks SyncHooks
	Sync  *synchronizer.Adapter
	Cfg   *config.Config
	// Logs is the process logger the log settings are applied to (see log_settings.go). Nil when the process logs
	// through the default handler only, as the OpenAPI printer and the tests that build an App directly do.
	Logs *logx.Switch
	// logVersion is the version of the log settings this process applied.
	logVersion atomic.Int64
	// logRevision is the revision_state revision of the log settings this process applied; logLoading is set while
	// a load runs (see noteLogSettingsRevision).
	logRevision atomic.Int64
	logLoading  atomic.Bool
	// jobFlights makes one poller of a synchronization job serve all the waiters of the job in this process.
	jobFlights singleflight.Group
	// syncAsked remembers when this process last asked for the refresh of a type (see requestInBackground).
	syncAsked sync.Map
	// dbos is the DBOS handle of the process: the client that enqueues the synchronization jobs, or the worker's runtime.
	dbos dbosState
	// answers collapses the concurrent computations of the same answer (see cachedAnswer).
	answers singleflight.Group
	// bg runs the background work (cache writes, refreshes) on a bounded pool, started on first use; Close waits for it.
	bg     *backgroundPool
	bgOnce sync.Once
	// HTTP is the client for outgoing requests (IdP, JWKS).
	// Timeouts are set by the context of each call.
	HTTP    *http.Client
	metrics *metrics

	// tune is the effective value of the settings of the fast read path.
	tune tuning
	// answersL1 is the in-process level of the answer cache. Nil on an App built by hand: it keeps nothing.
	answersL1 *lruCache[any]
	// jwks holds the key sets of the identity providers, configured on first use (see keySets).
	jwks     pepauth.KeySets
	jwksOnce sync.Once
	// adminVerifier checks the tokens of the administrators (ADMIN_AUTH__*), made on first use (see VerifyAdminToken).
	adminVerifier     *pepauth.Verifier
	adminVerifierOnce sync.Once
	// local keeps the revision of the policy and its snapshot in memory (see localView).
	local viewState
	// pep keeps the PEP directory of the process (see PEPDirectory).
	pep pepState
	// ready answers the readiness probe (see Ready); health tells which dependencies the calls are failing on lately.
	ready  readyState
	health struct{ postgres, redis dependencyHealth }
	// admit counts the requests in flight and the ones refused (see Admit).
	admit admission
	// attrsMemo is the attribute names of the last revision read, for a process without the snapshot.
	attrsMemo atomic.Pointer[attrsMemo]
	// computeSlots bounds the computations of answers that are not cached (see cachedAnswer).
	computeSlots chan struct{}
	// snapshotWarn logs once that the policy does not fit in memory.
	snapshotWarn sync.Once
	// clock is the time of the decisions and of the answer cache; nil is time.Now. Tests replace it (SetClock) to follow
	// hours of validity windows at once, or to ask a question at a given moment.
	clock func() time.Time
}

// now is the time the decisions are made at (which rules and memberships are in force) and the answer cache reasons with
// (see cachedAnswer).
func (a *App) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// SetClock makes the App decide at the times now returns instead of time.Now (nil goes back to time.Now): the validity
// windows of the rules and the memberships, the hour and the weekday the conditions read, and the times of the answer
// cache. It is for the tests that ask a question at a given moment through the real request path (the decision contracts);
// it must be called before the App serves a request, and now must be safe for concurrent use.
func (a *App) SetClock(now func() time.Time) { a.clock = now }

// NewEnv is the environment of a request decided by this App: the claims, the request attributes and the time of its clock.
func (a *App) NewEnv(claims Claims, context map[string]any) Env {
	return Env{Claims: claims, Context: context, Now: a.now()}
}

func New(pool *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) *App {
	m := newMetrics()
	adapter := synchronizer.NewAdapter(rdb)
	adapter.DefaultFetchTTL = cfg.Sync.FetchTTL
	adapter.OnBreakerOpen = func() { m.breakerOpen.WithLabelValues("source").Inc() }
	t := newTuning(cfg)
	app := &App{
		DB:           pool,
		Redis:        rdb,
		Sync:         adapter,
		Cfg:          cfg,
		HTTP:         &http.Client{},
		metrics:      m,
		tune:         t,
		answersL1:    newLRU[any](t.answerL1Size),
		computeSlots: make(chan struct{}, t.computeConcurrency),
	}
	// Every statement and every Redis round trip of the process is counted: in the metrics, and in the Trace of the
	// request that made it. A pool not made by db.Connect has no tracer and is not counted.
	db.Observe(pool, observer{app})
	redisx.Observe(rdb, observer{app})
	m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "licetum_redis_skipped_total",
		Help: "Redis commands not sent because the fast-fail was open after repeated errors or timeouts.",
	}, func() float64 { return float64(redisx.Skipped(rdb)) }))
	m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "licetum_log_suppressed_total",
		Help: "Log records left out because the same failure of a dependency had just been logged (one record per kind of failure per second).",
	}, func() float64 { return float64(logx.Repeated.Suppressed()) }))
	m.registry.MustRegister(&syncQueueCollector{app: app})
	m.registry.MustRegister(&poolCollector{app: app})
	m.registry.MustRegister(&degradedCollector{app: app})
	app.registerAdmissionMetrics(m)
	return app
}

// Close stops new background work, waits for the work in progress (a little while), then closes the connections
// to the SQL sources.
func (a *App) Close() {
	if !a.background().close(closeWait) {
		slog.Warn("background work did not finish before the shutdown", "wait", closeWait)
	}
	a.closeDBOS()
	if a.Sync != nil {
		a.Sync.Close()
	}
}

// AfterWrite is called once a write that can change a decision has committed, with the revision the write took
// (db.BumpRevision, in the write's own transaction): it is the revision of the write for the caller (X-Authz-Revision),
// it makes this process read the revisions again at once, and it makes the types of the platform itself due for synchronization (the
// entities they are made of were just written, and a write must show at once; the data of the other services is not
// touched: a change of a type or of a synchronizer changes its structure signature instead). The replicas of the previous release follow the Redis counter, which is bumped for them while a rolling
// update lasts. The write is committed by now, so a Redis failure is not turned into a request error.
func (a *App) AfterWrite(ctx context.Context, revision int64) {
	if t := TraceFrom(ctx); t != nil {
		t.setRevision(revision)
	}
	a.invalidateView()
	if _, err := redisx.BumpVersion(ctx, a.Redis); err != nil {
		logRedis(ctx, slog.LevelWarn, "failed to bump the cache version of the previous release", err)
	}
	if err := a.invalidatePlatformSync(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to make the platform types due for synchronization", "error", err)
	}
}

// ---------- shared existence checks ----------

const ServiceColumns = `id, name, slug, url, icon`

func ScanService(row pgx.Row) (*model.Service, error) {
	var s model.Service
	err := row.Scan(&s.ID, &s.Name, &s.Slug, &s.URL, &s.Icon)
	return &s, err
}

// ServiceIDByRef returns the id of the service a path segment names: its slug. A
// slug is never a UUID (see model.ValidSlug), so the two cannot be confused.
func (a *App) ServiceIDByRef(ctx context.Context, ref string) (uuid.UUID, error) {
	if snap := a.policySnapshot(ctx); snap != nil {
		return snap.serviceID(ref)
	}
	var id uuid.UUID
	err := a.DB.QueryRow(ctx, `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL`, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.NotFound(i18n.ServiceNotFound, ref)
	}
	return id, err
}

// serviceExists checks that the service is live, from the snapshot of the state when there is one.
func (a *App) serviceExists(ctx context.Context, state cacheState, serviceID uuid.UUID) error {
	if state.snap != nil {
		_, err := state.snap.service(serviceID)
		return err
	}
	_, err := a.GetExistingService(ctx, a.DB, serviceID)
	return err
}

func (a *App) GetExistingService(ctx context.Context, q db.Querier, serviceID uuid.UUID) (*model.Service, error) {
	s, err := ScanService(q.QueryRow(ctx,
		`SELECT `+ServiceColumns+` FROM service WHERE id = $1 AND deleted_at IS NULL`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.ServiceNotFound, serviceID)
	}
	return s, err
}

const TypeResourceColumns = `id, service_id, name, access_implies`

// ScanTypeResource reads the typeResourceColumns columns plus any extra dest; the parents are
// attached by loadTypeParents.
func ScanTypeResource(row pgx.Row, extra ...any) (*model.TypeResource, error) {
	var t model.TypeResource
	var implies []byte
	dest := append([]any{&t.ID, &t.ServiceID, &t.Name, &implies}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	t.Parents = []model.TypeParent{}
	t.AccessImplies = map[string][]string{}
	if len(implies) > 0 {
		if err := json.Unmarshal(implies, &t.AccessImplies); err != nil {
			return nil, err
		}
		if t.AccessImplies == nil {
			t.AccessImplies = map[string][]string{}
		}
	}
	return &t, nil
}

// LoadTypeParents attaches the parent links to the given types (one query for all of them).
func (a *App) LoadTypeParents(ctx context.Context, q db.Querier, types []*model.TypeResource) error {
	if len(types) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(types))
	byID := make(map[uuid.UUID]*model.TypeResource, len(types))
	for i, t := range types {
		ids[i] = t.ID
		byID[t.ID] = t
	}
	rows, err := q.Query(ctx,
		`SELECT type_resource_id, parent_id, inherit FROM type_resource_parent
		 WHERE type_resource_id = ANY($1) ORDER BY parent_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var child uuid.UUID
		var p model.TypeParent
		if err := rows.Scan(&child, &p.TypeResourceID, &p.Inherit); err != nil {
			return err
		}
		t := byID[child]
		t.Parents = append(t.Parents, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range types {
		if len(t.Parents) == 1 {
			id := t.Parents[0].TypeResourceID
			t.TypeResourceParentID = &id
		}
	}
	return nil
}

// getTypeResourceAnyState returns a resource type by id, including soft-deleted ones (nil if no row).
func (a *App) getTypeResourceAnyState(ctx context.Context, q db.Querier, id uuid.UUID) (*model.TypeResource, bool, error) {
	var deleted bool
	t, err := ScanTypeResource(q.QueryRow(ctx,
		`SELECT `+TypeResourceColumns+`, deleted_at IS NOT NULL FROM type_resource WHERE id = $1`, id), &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := a.LoadTypeParents(ctx, q, []*model.TypeResource{t}); err != nil {
		return nil, false, err
	}
	return t, deleted, nil
}

func (a *App) GetExistingTypeResource(ctx context.Context, q db.Querier, serviceID, typeResourceID uuid.UUID) (*model.TypeResource, error) {
	t, deleted, err := a.getTypeResourceAnyState(ctx, q, typeResourceID)
	if err != nil {
		return nil, err
	}
	if t == nil || deleted || t.ServiceID != serviceID {
		return nil, apperr.NotFound(i18n.TypeResourceNotFound, typeResourceID)
	}
	return t, nil
}

// GetExistingTypeResourceAnyService is like getExistingTypeResource but not bound to a service: for
// references that may structurally point to a resource type of another service.
func (a *App) GetExistingTypeResourceAnyService(ctx context.Context, q db.Querier, typeResourceID uuid.UUID) (*model.TypeResource, error) {
	t, deleted, err := a.getTypeResourceAnyState(ctx, q, typeResourceID)
	if err != nil {
		return nil, err
	}
	if t == nil || deleted {
		return nil, apperr.NotFound(i18n.TypeResourceNotFound, typeResourceID)
	}
	return t, nil
}

func (a *App) GetExistingGroup(ctx context.Context, q db.Querier, groupID uuid.UUID) (*model.Group, error) {
	var g model.Group
	err := q.QueryRow(ctx, `SELECT id, name FROM user_group WHERE id = $1`, groupID).Scan(&g.ID, &g.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.GroupNotFound, groupID)
	}
	return &g, err
}

func (a *App) GetExistingRole(ctx context.Context, q db.Querier, roleID uuid.UUID) (*model.Role, error) {
	var r model.Role
	err := q.QueryRow(ctx, `SELECT id, name FROM role WHERE id = $1`, roleID).Scan(&r.ID, &r.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.RoleNotFound, roleID)
	}
	return &r, err
}

func Exists(ctx context.Context, q db.Querier, sql string, args ...any) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (`+sql+`)`, args...).Scan(&ok)
	return ok, err
}

// ---------- small helpers ----------

func NullUUID(v uuid.NullUUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := v.UUID
	return &id
}

func ToNullUUID(v *uuid.UUID) uuid.NullUUID {
	if v == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *v, Valid: true}
}

func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // we only serialize our own types — an error here is a bug
	}
	return string(b)
}
