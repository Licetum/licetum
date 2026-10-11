// Package synchronizer holds the resource source adapters (an SQL query or an HTTP endpoint of the
// service owning the resource type). Every call is wrapped in a circuit breaker (Redis) and
// singleflight, which collapses concurrent identical requests into one.
package synchronizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

const (
	fetchOneTimeout = 5 * time.Second
	fetchAllTimeout = 10 * time.Second
	sqlPoolMaxConns = 4
)

// ErrSourceUnavailable is a transport failure of the source (network/DNS/syntax/response format).
// It differs from a legitimate "not found" so that the circuit breaker does not open on ordinary
// point misses.
var ErrSourceUnavailable = errors.New("synchronizer source unavailable")

func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSourceUnavailable, fmt.Sprintf(format, args...))
}

type Adapter struct {
	rdb  *redis.Client
	http *http.Client
	sf   singleflight.Group

	// DefaultFetchTTL is how long a fetched list of resources is kept in Redis when the synchronizer has
	// no cache_ttl of its own (config.Sync.FetchTTL). Zero — no cache.
	DefaultFetchTTL time.Duration

	// OnBreakerOpen, if set, is called whenever a call is skipped because the circuit breaker of a
	// source is open (a metrics hook).
	OnBreakerOpen func()

	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

func NewAdapter(rdb *redis.Client) *Adapter {
	return &Adapter{
		rdb:   rdb,
		http:  &http.Client{},
		pools: map[string]*pgxpool.Pool{},
	}
}

// NewAdapterWithHTTPClient configures the adapter with a caller-owned HTTP client. It is useful
// when a caller needs custom transport or timeout settings and keeps HTTP source tests in-process.
func NewAdapterWithHTTPClient(rdb *redis.Client, client *http.Client) *Adapter {
	adapter := NewAdapter(rdb)
	if client != nil {
		adapter.http = client
	}
	return adapter
}

// Close closes the cached connection pools to SQL sources.
func (a *Adapter) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for dsn, pool := range a.pools {
		pool.Close()
		delete(a.pools, dsn)
	}
}

// FetchOne returns a resource by external id, or nil if it does not exist or the source is unavailable.
// breakerKey == "" disables the circuit breaker and singleflight (checking a source before saving).
func (a *Adapter) FetchOne(ctx context.Context, cfg model.SynchronizerConfig, externalID, breakerKey string) *model.Resource {
	do := func(ctx context.Context) (*model.Resource, error) {
		ctx, cancel := context.WithTimeout(ctx, fetchOneTimeout)
		defer cancel()
		if cfg.Kind == model.KindSQL {
			return a.fetchOneSQL(ctx, cfg, externalID)
		}
		return a.fetchOneHTTP(ctx, cfg, externalID)
	}

	if breakerKey == "" {
		row, err := do(ctx)
		if err != nil {
			slog.WarnContext(ctx, "synchronizer fetch_one failed", "error", err)
			return nil
		}
		return row
	}

	if a.breakerOpen(ctx, breakerKey) {
		return nil
	}

	v, err, _ := a.sf.Do("fetch_one:"+breakerKey+":"+externalID, func() (any, error) {
		// Detach from the first caller's cancellation — every waiter needs the result.
		return do(context.WithoutCancel(ctx))
	})
	if err != nil {
		slog.WarnContext(ctx, "synchronizer fetch_one failed", "breaker", breakerKey, "error", err)
		redisx.RecordFailure(ctx, a.rdb, breakerKey)
		return nil
	}
	redisx.RecordSuccess(ctx, a.rdb, breakerKey)
	return v.(*model.Resource)
}

func (a *Adapter) breakerOpen(ctx context.Context, key string) bool {
	open := redisx.BreakerOpen(ctx, a.rdb, key)
	if open && a.OnBreakerOpen != nil {
		a.OnBreakerOpen()
	}
	return open
}

// FetchAll returns the full resource list of the source. An error means the source did not answer: a transport
// failure, a bad status, a malformed answer, or an open circuit breaker. An answer that lists no resource is an
// answer like any other — an empty list with no error — and the caller takes it as the source's truth. The list is
// kept in Redis for the synchronizer's cache_ttl (or DefaultFetchTTL), an empty one included. fresh (consistency=higher)
// asks the source anyway and refreshes the cached list. Checks before saving (breakerKey == "") always go to the source.
func (a *Adapter) FetchAll(ctx context.Context, cfg model.SynchronizerConfig, breakerKey string, fresh bool) ([]model.Resource, error) {
	do := func(ctx context.Context) ([]model.Resource, error) {
		ctx, cancel := context.WithTimeout(ctx, fetchAllTimeout)
		defer cancel()
		if cfg.Kind == model.KindSQL {
			return a.fetchAllSQL(ctx, cfg)
		}
		return a.fetchAllHTTP(ctx, cfg)
	}

	if breakerKey == "" {
		rows, err := do(ctx)
		if err != nil {
			slog.WarnContext(ctx, "synchronizer fetch_all failed", "error", err)
		}
		return rows, err
	}

	ttl := a.fetchTTL(cfg)
	key := fetchCacheKey(cfg, breakerKey)
	if ttl > 0 && !fresh {
		if rows, ok := a.cachedRows(ctx, key); ok {
			return rows, nil
		}
	}

	if a.breakerOpen(ctx, breakerKey) {
		return nil, unavailable("the circuit breaker of the source is open")
	}

	flight := "fetch_all:" + breakerKey
	if fresh {
		flight = "fetch_all:fresh:" + breakerKey
	}
	v, err, _ := a.sf.Do(flight, func() (any, error) {
		rows, err := do(context.WithoutCancel(ctx))
		if err != nil {
			return nil, err
		}
		a.storeRows(context.WithoutCancel(ctx), key, rows, ttl)
		return rows, nil
	})
	if err != nil {
		slog.WarnContext(ctx, "synchronizer fetch_all failed", "breaker", breakerKey, "error", err)
		redisx.RecordFailure(ctx, a.rdb, breakerKey)
		return nil, err
	}
	redisx.RecordSuccess(ctx, a.rdb, breakerKey)
	rows, _ := v.([]model.Resource)
	return rows, nil
}

// fetchTTL is how long a fetched list is kept: the synchronizer's own cache_ttl, or the default.
func (a *Adapter) fetchTTL(cfg model.SynchronizerConfig) time.Duration {
	if cfg.CacheTTL != nil {
		return time.Duration(*cfg.CacheTTL) * time.Second
	}
	return a.DefaultFetchTTL
}

// fetchCacheKey names a cached list of a source. The fingerprint covers the configuration with its secrets
// masked (the DSN password and the header values): a plain hash of the secrets would let whoever can read
// Redis guess them offline. Editing the synchronizer's queries or URL still cuts off a list fetched with the
// old configuration; changing only a secret keeps the cached list for at most its TTL.
func fetchCacheKey(cfg model.SynchronizerConfig, breakerKey string) string {
	raw, _ := json.Marshal(cfg.Masked())
	sum := sha256.Sum256(raw)
	return "authz:fetch_all:" + breakerKey + ":" + hex.EncodeToString(sum[:])
}

// cachedRows reads a list kept in Redis. The cache is best-effort: a miss, an error or a broken value
// is a miss.
func (a *Adapter) cachedRows(ctx context.Context, key string) ([]model.Resource, bool) {
	if a.rdb == nil {
		return nil, false
	}
	raw, err := a.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var rows []model.Resource
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, false
	}
	return rows, true
}

// storeRows keeps a fetched list for ttl. An empty list is kept too: the source answered with it.
func (a *Adapter) storeRows(ctx context.Context, key string, rows []model.Resource, ttl time.Duration) {
	if a.rdb == nil || ttl <= 0 {
		return
	}
	if rows == nil {
		rows = []model.Resource{}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return
	}
	if err := a.rdb.Set(ctx, key, raw, ttl).Err(); err != nil {
		slog.WarnContext(ctx, "failed to cache the resources of a synchronizer", "error", err)
	}
}

// ---------- SQL ----------

func (a *Adapter) pool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.pools[dsn]; ok {
		return p, nil
	}
	cfg, err := pgxpool.ParseConfig(db.NormalizeDSN(dsn))
	if err != nil {
		return nil, unavailable("parse dsn: %v", err)
	}
	cfg.MaxConns = sqlPoolMaxConns
	// Simple protocol: parameters are inlined as literals and cast by PostgreSQL to the column type
	// (so `WHERE id = :id` works for uuid, int and text columns alike — as with asyncpg/SQLAlchemy).
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, unavailable("connect: %v", err)
	}
	a.pools[dsn] = p
	return p, nil
}

// BindIDParam turns the SQLAlchemy named parameter `:id` into the positional `$1`
// (same rules as sqlalchemy.text: `::type` is a cast, not a parameter; `\:` is a literal colon).
func BindIDParam(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\\' && i+1 < len(query) && query[i+1] == ':' {
			b.WriteByte(':')
			i++
			continue
		}
		if c == ':' && strings.HasPrefix(query[i+1:], "id") {
			prevOK := i == 0 || (query[i-1] != ':' && !isWord(query[i-1]))
			end := i + 3
			nextOK := end >= len(query) || (!isWord(query[end]) && query[end] != ':')
			if prevOK && nextOK {
				b.WriteString("$1")
				i += 2
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (a *Adapter) fetchOneSQL(ctx context.Context, cfg model.SynchronizerConfig, externalID string) (*model.Resource, error) {
	p, err := a.pool(ctx, cfg.DSN)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, BindIDParam(cfg.Query), externalID)
	if err != nil {
		return nil, unavailable("query: %v", err)
	}
	resources, err := collectSQL(rows)
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, nil
	}
	resource := &resources[0]
	if cfg.ParentsQuery != "" {
		prows, err := p.Query(ctx, BindIDParam(cfg.ParentsQuery), externalID)
		if err != nil {
			return nil, unavailable("parents query: %v", err)
		}
		links, err := collectParents(prows, false)
		if err != nil {
			return nil, err
		}
		resource.Parents = links[""]
	}
	return resource, nil
}

func (a *Adapter) fetchAllSQL(ctx context.Context, cfg model.SynchronizerConfig) ([]model.Resource, error) {
	if cfg.ListQuery == "" {
		return nil, unavailable("the synchronizer has no list query")
	}
	p, err := a.pool(ctx, cfg.DSN)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, cfg.ListQuery)
	if err != nil {
		return nil, unavailable("query: %v", err)
	}
	resources, err := collectSQL(rows)
	if err != nil {
		return nil, err
	}
	if cfg.ParentsListQuery != "" {
		prows, err := p.Query(ctx, cfg.ParentsListQuery)
		if err != nil {
			return nil, unavailable("parents query: %v", err)
		}
		links, err := collectParents(prows, true)
		if err != nil {
			return nil, err
		}
		for i := range resources {
			resources[i].Parents = links[resources[i].ID]
		}
	}
	return resources, nil
}

// collectParents reads the parent links of resources: the columns parent_type and parent_id, plus id
// (the child) when withID is set. The result is keyed by the child id ("" when withID is not set).
func collectParents(rows pgx.Rows, withID bool) (map[string][]model.ResourceParent, error) {
	defer rows.Close()
	idx := map[string]int{}
	for i, fd := range rows.FieldDescriptions() {
		idx[fd.Name] = i
	}
	typeCol, okType := idx["parent_type"]
	parentCol, okParent := idx["parent_id"]
	idCol, okID := idx["id"]
	if !okType || !okParent || (withID && !okID) {
		if withID {
			return nil, unavailable("the parents query must return the columns id, parent_type, parent_id")
		}
		return nil, unavailable("the parents query must return the columns parent_type, parent_id")
	}

	out := map[string][]model.ResourceParent{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, unavailable("read row: %v", err)
		}
		parentType, parentID := stringify(values[typeCol]), stringify(values[parentCol])
		if parentType == nil || parentID == nil {
			continue // a NULL link is no link
		}
		key := ""
		if withID {
			id := stringify(values[idCol])
			if id == nil {
				continue
			}
			key = *id
		}
		out[key] = append(out[key], model.ResourceParent{Type: *parentType, ID: *parentID})
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("query: %v", err)
	}
	return out, nil
}

func collectSQL(rows pgx.Rows) ([]model.Resource, error) {
	defer rows.Close()
	idx := map[string]int{}
	for i, fd := range rows.FieldDescriptions() {
		idx[fd.Name] = i
	}
	idCol, okID := idx["id"]
	nameCol, okName := idx["name"]
	parentCol, okParent := idx["parent_id"]
	if !okID || !okName || !okParent {
		return nil, unavailable("the query must return the columns id, name, parent_id")
	}
	// Every other column is an attribute of the resource (see model.Resource.Meta).
	fields := rows.FieldDescriptions()
	var extra []int
	for i := range fields {
		if i != idCol && i != nameCol && i != parentCol {
			extra = append(extra, i)
		}
	}

	var out []model.Resource
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, unavailable("read row: %v", err)
		}
		id := stringify(values[idCol])
		if id == nil {
			return nil, unavailable("resource id must not be NULL")
		}
		name := stringify(values[nameCol])
		if name == nil {
			return nil, unavailable("resource name must not be NULL")
		}
		r := model.Resource{ID: *id, Name: *name, ParentID: stringify(values[parentCol])}
		if len(extra) > 0 {
			attrs := make(map[string]any, len(extra))
			for _, i := range extra {
				attrs[fields[i].Name] = values[i]
			}
			r.Meta = normalizeMeta(attrs)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable("query: %v", err)
	}
	return out, nil
}

// ---------- HTTP ----------

// getJSON performs GET rawURL with the configured headers. The header values may be secrets: neither
// they nor the config end up in errors or logs.
func (a *Adapter) getJSON(ctx context.Context, rawURL string, headers map[string]string, dst any) (status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, unavailable("request: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, unavailable("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, unavailable("HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<20))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return resp.StatusCode, unavailable("invalid JSON: %v", err)
	}
	return resp.StatusCode, nil
}

func (a *Adapter) fetchOneHTTP(ctx context.Context, cfg model.SynchronizerConfig, externalID string) (*model.Resource, error) {
	var data map[string]any
	status, err := a.getJSON(ctx, strings.TrimRight(cfg.URL, "/")+"/"+url.PathEscape(externalID), cfg.Headers, &data)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	r, err := resourceFromJSON(data)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (a *Adapter) fetchAllHTTP(ctx context.Context, cfg model.SynchronizerConfig) ([]model.Resource, error) {
	var data []map[string]any
	status, err := a.getJSON(ctx, cfg.URL, cfg.Headers, &data)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, unavailable("HTTP 404")
	}
	if data == nil { // "null" is not a list
		return nil, unavailable("the response is not a list")
	}
	out := make([]model.Resource, 0, len(data))
	for _, item := range data {
		r, err := resourceFromJSON(item)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func resourceFromJSON(data map[string]any) (model.Resource, error) {
	id := stringify(data["id"])
	if id == nil {
		return model.Resource{}, unavailable("the response has no id")
	}
	name, ok := data["name"].(string)
	if !ok {
		return model.Resource{}, unavailable("the response has no string name")
	}
	r := model.Resource{ID: *id, Name: name, ParentID: stringify(data["parent_id"])}
	// Attributes: the fields besides the fixed ones, and the entries of an explicit meta object.
	attrs := map[string]any{}
	for k, v := range data {
		switch k {
		case "id", "name", "parent_id", "parents", "meta":
		default:
			attrs[k] = v
		}
	}
	if explicit, ok := data["meta"].(map[string]any); ok {
		for k, v := range explicit {
			attrs[k] = v
		}
	}
	r.Meta = normalizeMeta(attrs)
	if raw, ok := data["parents"]; ok && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return model.Resource{}, unavailable("parents must be an array")
		}
		for _, item := range items {
			link, ok := item.(map[string]any)
			if !ok {
				return model.Resource{}, unavailable("parents must be an array of {type, id}")
			}
			parentType, parentID := stringify(link["type"]), stringify(link["id"])
			if parentType == nil || parentID == nil {
				return model.Resource{}, unavailable("every parent needs a type and an id")
			}
			r.Parents = append(r.Parents, model.ResourceParent{Type: *parentType, ID: *parentID})
		}
	}
	return r, nil
}

// Limits on the attributes of a resource: what exceeds them is dropped, one bad attribute must not
// make the whole source unusable.
const (
	maxMetaKeys        = 32
	maxMetaValueLength = 1024
	maxMetaListItems   = 64
)

// normalizeMeta reduces the attributes a source reported to what rules can match: a string (numbers
// and booleans are rendered like claim values) or a list of strings. NULLs, objects, invalid names and
// oversized values are dropped. Returns nil if nothing is left.
func normalizeMeta(attrs map[string]any) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if len(out) >= maxMetaKeys {
			break
		}
		if !model.ValidMetaKey(k) {
			continue
		}
		switch v := attrs[k].(type) {
		case nil, map[string]any:
		case []any:
			items := make([]any, 0, len(v))
			for _, item := range v {
				if s := stringify(item); s != nil && len(*s) <= maxMetaValueLength && len(items) < maxMetaListItems {
					items = append(items, *s)
				}
			}
			out[k] = items
		case []string:
			items := make([]any, 0, len(v))
			for _, item := range v {
				if len(item) <= maxMetaValueLength && len(items) < maxMetaListItems {
					items = append(items, item)
				}
			}
			out[k] = items
		default:
			if s := stringify(v); s != nil && len(*s) <= maxMetaValueLength {
				out[k] = *s
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringify mirrors str(value) of the Python version for values from SQL/JSON; nil → nil.
func stringify(v any) *string {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s = x
	case []byte:
		s = string(x)
	case [16]byte:
		s = uuid.UUID(x).String()
	case uuid.UUID:
		s = x.String()
	case json.Number:
		s = x.String()
	case *big.Int:
		s = x.String()
	case bool:
		if x {
			s = "True"
		} else {
			s = "False"
		}
	case fmt.Stringer:
		s = x.String()
	default:
		s = fmt.Sprint(x)
	}
	return &s
}
