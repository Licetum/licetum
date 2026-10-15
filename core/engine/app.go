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
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/model"
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
	// logVersion is the version of the log settings this process applied (see WatchLogSettings).
	logVersion atomic.Int64
	// jobFlights makes one poller of a synchronization job serve all the waiters of the job in this process.
	jobFlights singleflight.Group
	// syncAsked remembers when this process last asked for the refresh of a type (see requestInBackground).
	syncAsked sync.Map
	// workerWake wakes the workers of this process when a job is queued (the other processes get a NOTIFY).
	workerWake chan struct{}
	// doneMu guards doneCh, which is closed when a job ends in this process.
	doneMu sync.Mutex
	doneCh chan struct{}
	// answers collapses the concurrent computations of the same answer (see cachedAnswer).
	answers singleflight.Group
	// delegationPruned is when the retention of the delegated-call audit was last applied (unix nanoseconds).
	delegationPruned atomic.Int64
	// subjectNotes remembers which subjects were written to the registry lately (see NoteSubject).
	subjectNotes subjectNotes
	// inflight counts the background work (cache writes, refreshes); Close waits for it.
	inflight sync.WaitGroup
	// closeMu guards closed: once Close has begun, no background work starts (see background).
	closeMu sync.Mutex
	closed  bool
	// HTTP is the client for outgoing requests (IdP, JWKS).
	// Timeouts are set by the context of each call.
	HTTP    *http.Client
	metrics *metrics
}

func New(pool *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) *App {
	m := newMetrics()
	adapter := synchronizer.NewAdapter(rdb)
	adapter.DefaultFetchTTL = cfg.Sync.FetchTTL
	adapter.OnBreakerOpen = func() { m.breakerOpen.WithLabelValues("source").Inc() }
	a := &App{
		DB:         pool,
		Redis:      rdb,
		Sync:       adapter,
		Cfg:        cfg,
		HTTP:       &http.Client{},
		metrics:    m,
		workerWake: make(chan struct{}, 1),
	}
	m.registry.MustRegister(&syncQueueCollector{app: a})
	return a
}

// Close stops new background work, waits for the work in progress (a little while), then closes the connections
// to the SQL sources.
func (a *App) Close() {
	const closeWait = 10 * time.Second
	a.closeMu.Lock()
	a.closed = true
	a.closeMu.Unlock()
	done := make(chan struct{})
	go func() {
		a.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeWait):
		slog.Warn("background work did not finish before the shutdown", "wait", closeWait)
	}
	a.Sync.Close()
}

// BumpVersion invalidates the permission cache and makes the types of the platform itself due for synchronization (the
// entities they are made of were just written, and a write must show at once). The data of the other services is not
// touched: a change of a type or of a synchronizer changes its structure signature, which is what makes it due. The DB
// write has already been committed by now, so a failure here is not turned into a request error — at worst the cache
// lives until its TTL expires.
func (a *App) BumpVersion(ctx context.Context) {
	version, err := redisx.BumpVersion(ctx, a.Redis)
	if err != nil {
		slog.ErrorContext(ctx, "failed to bump the permission cache version", "error", err)
	} else if t := TraceFrom(ctx); t != nil {
		t.setRevision(version)
	}
	if err := a.invalidatePlatformSync(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to make the platform types due for synchronization", "error", err)
	}
}

// ---------- shared existence checks ----------

const ServiceColumns = `id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients`

func ScanService(row pgx.Row) (*model.Service, error) {
	var s model.Service
	err := row.Scan(&s.ID, &s.Name, &s.Slug, &s.URL, &s.Icon, &s.AuthorizationURL, &s.TokenURL, &s.JwksURL,
		&s.Issuer, &s.ClientID, &s.ClientSecret, &s.TrustedClients)
	return &s, err
}

// ServiceIDByRef returns the id of the service a path segment names: the id itself or the slug. A
// slug is never a UUID (see model.ValidSlug), so the two cannot be confused.
func (a *App) ServiceIDByRef(ctx context.Context, ref string) (uuid.UUID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return id, nil
	}
	var id uuid.UUID
	err := a.DB.QueryRow(ctx, `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL`, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.NotFound(i18n.ServiceNotFound, ref)
	}
	return id, err
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
