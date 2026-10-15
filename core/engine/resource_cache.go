package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// resource_cache is the local copy of the resource graph fetched by the synchronizers:
//
//   - resource_cache: the resources themselves (active = still present in the source; a resource
//     that disappeared is switched off, not deleted, so one bad read of a source does not punch
//     holes in permissions);
//   - resource_edge: the declared parents of each resource, many-to-many, of any parent type;
//     parent_id is the resolved parent row, NULL while the parent is not in the cache;
//   - resource_ancestor: the transitive closure of the edges (see core/db/migrations/00001_init.sql): one row per
//     (ancestor, descendant) pair carrying the descendant's type, external id and name, so that the
//     descendants of an anchor are one index range scan.
//
// The permission computation works on this copy only and never asks a source per resource.

const (
	// syncHashSalt: changing it drops every stored hash (e.g. when the way the closure is built
	// changes) — the next access rebuilds every type.
	syncHashSalt   = "v4"
	syncHashPrefix = "authz:resource_sync:hash:"
	syncLastPrefix = "authz:resource_sync:last:"
	// syncEpochKey grows on every write that changes what a synchronization produces (types,
	// synchronizers): it is part of the throttle key, so such a write ends the throttle at once.
	syncEpochKey = "authz:sync_epoch"
	// syncTTL is how long a type's source is not asked again after a synchronization, unless the
	// caller asks for higher consistency.
	syncTTL = 30 * time.Second
	// syncWriteLockKey is the advisory lock key: writers of the graph (also on different replicas)
	// go strictly one after another, otherwise two transactions rebuilding the closure could deadlock.
	syncWriteLockKey = 7_303_001
	// insertChunkSize is the number of rows per INSERT built from unnest arrays.
	insertChunkSize = 5000
)

// EdgeRef is a declared parent of a resource, resolved to a parent type.
type EdgeRef struct {
	ParentType       uuid.UUID
	ParentExternalID string
}

// IncomingResource is a source resource with its parents resolved against the parent types of its type.
type IncomingResource struct {
	ExternalID string
	Name       string
	Edges      []EdgeRef
	Meta       map[string]any // attributes: strings and lists of strings, never nil
}

// NormalizeResources resolves the parents a source reports to parent types of the resource type: the
// `type` of a parent is the name of one of the type's parent types (service/name if the name alone is
// ambiguous), and the legacy `parent_id` means the only parent type. Links that cannot be resolved
// are dropped and counted.
func NormalizeResources(g *TypeGraph, typeID uuid.UUID, rows []model.Resource) (out []IncomingResource, dropped int) {
	parents := g.liveParents(typeID)
	byName := map[string][]uuid.UUID{}
	for _, e := range parents {
		if n, ok := g.Node(e.Parent); ok {
			byName[n.Name] = append(byName[n.Name], n.ID)
			byName[n.ServiceName+"/"+n.Name] = append(byName[n.ServiceName+"/"+n.Name], n.ID)
		}
	}

	out = make([]IncomingResource, 0, len(rows))
	for _, r := range rows {
		item := IncomingResource{ExternalID: r.ID, Name: r.Name, Meta: r.Meta}
		if item.Meta == nil {
			item.Meta = map[string]any{}
		}
		seen := map[EdgeRef]bool{}
		add := func(parentType uuid.UUID, ext string) {
			ref := EdgeRef{ParentType: parentType, ParentExternalID: ext}
			if !seen[ref] {
				seen[ref] = true
				item.Edges = append(item.Edges, ref)
			}
		}
		for _, p := range r.Parents {
			if ids := byName[p.Type]; len(ids) == 1 {
				add(ids[0], p.ID)
			} else {
				dropped++
			}
		}
		if r.ParentID != nil && len(r.Parents) == 0 {
			if len(parents) == 1 {
				add(parents[0].Parent, *r.ParentID)
			} else {
				dropped++
			}
		}
		slices.SortFunc(item.Edges, func(a, b EdgeRef) int {
			return strings.Compare(a.ParentType.String()+a.ParentExternalID, b.ParentType.String()+b.ParentExternalID)
		})
		out = append(out, item)
	}
	return out, dropped
}

// dataDigest fingerprints a source answer (deduplicated by id). The bytes hashed are the answer written out in
// order; they are streamed into the hash, so a large answer is never copied into one string first.
func dataDigest(rows map[string]IncomingResource) string {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	h := sha256.New()
	for _, id := range ids {
		r := rows[id]
		writeString(h, r.ExternalID)
		h.Write([]byte{0})
		writeString(h, r.Name)
		for _, e := range r.Edges {
			h.Write([]byte{1})
			writeString(h, e.ParentType.String())
			writeString(h, ":")
			writeString(h, e.ParentExternalID)
		}
		h.Write([]byte{3})
		meta, _ := json.Marshal(r.Meta) // map keys are sorted: the encoding is canonical
		h.Write(meta)
		h.Write([]byte{2})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeString writes s to w. A hash never fails to write.
func writeString(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

// syncDigest is what is stored in Redis for a type: the hash of the data and the signature of the
// type graph above it. A changed graph part means every resource of the type must be recomputed even
// though the data did not change; a changed data part is diffed against the cache.
func syncDigest(data, graph string) string { return syncHashSalt + ":" + data + ":" + graph }

func splitDigest(digest string) (data, graph string) {
	parts := strings.Split(digest, ":")
	if len(parts) != 3 || parts[0] != syncHashSalt {
		return "", ""
	}
	return parts[1], parts[2]
}

// liveSynchronizers returns the synchronizer of each given type that has a live (not deleted) one.
// The order is fixed: if a type has several synchronizers (nothing forbids it), the same one is
// always taken — otherwise its resources would flicker.
func (a *App) liveSynchronizers(ctx context.Context, typeIDs []uuid.UUID) ([]model.Synchronizer, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT DISTINCT ON (type_resource_id) `+SynchronizerColumns+`
		FROM synchronizer
		WHERE type_resource_id = ANY($1) AND deleted_at IS NULL
		ORDER BY type_resource_id, id`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Synchronizer
	for rows.Next() {
		s, err := ScanSynchronizer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (a *App) ActiveCounts(ctx context.Context, typeIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT type_resource_id, count(*) FROM resource_cache
		WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

type changedType struct {
	typeID   uuid.UUID
	rows     []IncomingResource
	digest   string
	touchAll bool
}

// syncPlan is what a synchronization knows before it fetches anything: the live synchronizers of the types,
// which of them are within their throttle, and the hash each type was last synchronized with.
type syncPlan struct {
	epoch     string
	synced    map[uuid.UUID]bool   // the types with a live synchronizer
	syncs     []model.Synchronizer // their live synchronizers
	throttled map[uuid.UUID]bool   // fetched again within syncTTL: not due
	stored    map[uuid.UUID]string // the hash of the last synchronization ("" if never synchronized)
}

// throttleKey names the throttle of a type in the current sync epoch.
func (p *syncPlan) throttleKey(typeID uuid.UUID) string {
	return syncLastPrefix + p.epoch + ":" + typeID.String()
}

// planSync reads the state of the synchronization of the given types: their synchronizers, the throttle keys
// and the stored hashes, the Redis part in two round trips. It fetches nothing and writes nothing, so it runs
// without the synchronization lock.
func (a *App) planSync(ctx context.Context, typeIDs []uuid.UUID) (*syncPlan, error) {
	p := &syncPlan{synced: map[uuid.UUID]bool{}, throttled: map[uuid.UUID]bool{}, stored: map[uuid.UUID]string{}}
	syncs, err := a.liveSynchronizers(ctx, typeIDs)
	if err != nil {
		return nil, err
	}
	p.syncs = syncs
	for _, s := range syncs {
		p.synced[s.TypeResourceID] = true
	}
	if len(syncs) == 0 {
		return p, nil
	}

	epoch, err := a.Redis.Get(ctx, syncEpochKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.WarnContext(ctx, "failed to read the sync epoch", "error", err)
	}
	p.epoch = epoch

	pipe := a.Redis.Pipeline()
	throttle := make([]*redis.IntCmd, len(syncs))
	hashes := make([]*redis.StringCmd, len(syncs))
	for i, s := range syncs {
		throttle[i] = pipe.Exists(ctx, p.throttleKey(s.TypeResourceID))
		hashes[i] = pipe.Get(ctx, syncHashPrefix+s.TypeResourceID.String())
	}
	_, _ = pipe.Exec(ctx) // every reply is read below: a missing key is a reply, not a failure of the pipeline
	for i, s := range syncs {
		typeID := s.TypeResourceID
		if n, err := throttle[i].Result(); err == nil && n > 0 {
			p.throttled[typeID] = true
		}
		hash, err := hashes[i].Result()
		switch {
		case err == nil:
			p.stored[typeID] = hash
		case !errors.Is(err, redis.Nil):
			slog.WarnContext(ctx, "failed to read the resource sync hash", "error", err)
		}
	}
	return p, nil
}

// syncOutcome is what a synchronization run found out: the types that have a live synchronizer, and those whose source
// was asked and did not answer (their data stays as it was).
type syncOutcome struct {
	synced     map[uuid.UUID]bool
	unanswered map[uuid.UUID]bool
}

// EnsureSynced runs syncTypes and returns the types that have a live synchronizer.
func (a *App) EnsureSynced(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID, fresh bool) (map[uuid.UUID]bool, error) {
	out, err := a.syncTypes(ctx, g, typeIDs, fresh)
	return out.synced, err
}

// syncTypes brings the local copy of the given types up to the state of their sources. The types not
// synchronized within syncTTL are fetched (all of them with fresh), in parallel (the circuit breaker and
// singleflight live in the adapter), but the DB is written only if something changed: the hash of the answer
// is compared with the one stored in Redis. A source that did not answer is skipped; an answer with no resources is
// the source's truth and switches off every resource of the type. The planning state is
// read again here, under the synchronization lock: another replica may have synchronized meanwhile. The permission
// path runs it through syncTypesOnce; the admin paths (IsCovered, ListResources) run it directly, and the writes
// of the rows are serialized by the advisory lock.
//
// Returns the types that have a live synchronizer: only their data is current.
func (a *App) syncTypes(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID, fresh bool) (syncOutcome, error) {
	plan, err := a.planSync(ctx, typeIDs)
	if err != nil {
		return syncOutcome{}, err
	}
	var due []model.Synchronizer
	for _, s := range plan.syncs {
		if fresh || !plan.throttled[s.TypeResourceID] {
			due = append(due, s)
		}
	}
	if len(due) == 0 {
		return syncOutcome{synced: plan.synced}, nil
	}

	started := time.Now()
	fetched := make([][]model.Resource, len(due))
	unanswered := make([]bool, len(due))
	var wg sync.WaitGroup
	for i, s := range due {
		wg.Go(func() {
			rows, err := a.Sync.FetchAll(ctx, s.Config, s.ID.String(), fresh)
			fetched[i], unanswered[i] = rows, err != nil
		})
	}
	wg.Wait()
	fetchDuration := time.Since(started)
	a.metrics.syncFetch.Observe(fetchDuration.Seconds())

	dueIDs := make([]uuid.UUID, len(due))
	for i, s := range due {
		dueIDs[i] = s.TypeResourceID
	}
	counts, err := a.ActiveCounts(ctx, dueIDs)
	if err != nil {
		return syncOutcome{}, err
	}

	unansweredTypes := map[uuid.UUID]bool{}
	var changed []changedType
	var settled []uuid.UUID
	for i, s := range due {
		typeID := s.TypeResourceID
		if unanswered[i] {
			unansweredTypes[typeID] = true
			slog.WarnContext(ctx, "the source of a resource type did not answer, sync skipped",
				"type_resource_id", typeID)
			a.metrics.syncResults.WithLabelValues("unanswered").Inc()
			continue
		}

		normalized, dropped := NormalizeResources(g, typeID, fetched[i])
		if dropped > 0 {
			slog.WarnContext(ctx, "the source reports parents that cannot be resolved to a parent type of the resource type: "+
				"the parent type is unknown or ambiguous (use parents[].type, service/name if needed)",
				"type_resource_id", typeID, "links", dropped)
		}
		unique := make(map[string]IncomingResource, len(normalized))
		for _, r := range normalized {
			unique[r.ExternalID] = r
		}
		digest := syncDigest(dataDigest(unique), structureOf(g, typeID, s.Config))

		stored := plan.stored[typeID]
		if stored == digest && counts[typeID] == len(unique) {
			settled = append(settled, typeID)
			a.metrics.syncResults.WithLabelValues("unchanged").Inc()
			continue
		}

		_, storedGraph := splitDigest(stored)
		_, newGraph := splitDigest(digest)
		rows := make([]IncomingResource, 0, len(unique))
		for _, r := range unique {
			rows = append(rows, r)
		}
		changed = append(changed, changedType{typeID: typeID, rows: rows, digest: digest, touchAll: storedGraph != newGraph})
	}

	if len(changed) > 0 {
		writeStarted := time.Now()
		err = db.InTx(ctx, a.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, syncWriteLockKey); err != nil {
				return err
			}
			touched := map[uuid.UUID]struct{}{}
			for _, c := range changed {
				if err := applyType(ctx, tx, c.typeID, c.rows, c.touchAll, false, touched, nil); err != nil {
					return fmt.Errorf("sync type %s: %w", c.typeID, err)
				}
			}
			return rebuildClosure(ctx, tx, touched)
		})
		if err != nil {
			return syncOutcome{}, err
		}
		slog.InfoContext(ctx, "resource graph synchronized", "changed", len(changed), "types", len(due),
			"fetch_ms", fetchDuration.Milliseconds(), "write_ms", time.Since(writeStarted).Milliseconds())
	}

	// The changed types are settled too; the metrics count them once, here.
	changedIDs := make([]uuid.UUID, 0, len(changed))
	for _, c := range changed {
		changedIDs = append(changedIDs, c.typeID)
		settled = append(settled, c.typeID)
		a.metrics.syncResults.WithLabelValues("changed").Inc()
	}
	if len(settled) == 0 {
		return syncOutcome{synced: plan.synced, unanswered: unansweredTypes}, nil
	}

	// One round trip records the data change of the changed types — the answers that read their services are stale
	// from now on — then stores their hashes, and starts the throttle of every settled type. The data change goes
	// first: if the hash is lost, the next sync sees a difference and records the change again.
	pipe := a.Redis.Pipeline()
	recordDataChange(ctx, pipe, g, changedIDs)
	for _, c := range changed {
		pipe.Set(ctx, syncHashPrefix+c.typeID.String(), c.digest, 0)
	}
	for _, id := range settled {
		pipe.Set(ctx, plan.throttleKey(id), "1", syncTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to store the resource sync state", "error", err)
	}
	a.invalidateView() // the data versions moved: the next request reads them again
	return syncOutcome{synced: plan.synced, unanswered: unansweredTypes}, nil
}

// structureOf fingerprints what the stored data of a type depends on besides its source: the type graph above it
// (the closure is built from it) and the synchronizer configuration. A state stored under another structure is not
// current, so the next synchronization runs in the request (see syncWhileServing).
func structureOf(g *TypeGraph, typeID uuid.UUID, cfg model.SynchronizerConfig) string {
	// cache_ttl only sets how long the list of the source stays in Redis: it does not change the data.
	cfg = cfg.Masked()
	cfg.CacheTTL = nil
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	return g.signature(typeID) + "-" + hex.EncodeToString(sum[:8])
}

// syncWhileServing is the synchronization of a minimize_latency answer. A type that was synchronized before, under
// the structure it has now, is answered from its data as of the last synchronization, and refreshed in the
// background once its throttle has run out: the request does not wait for the source. Any other type — never
// synchronized, synchronized under another structure, or with no active rows and not emptied by the source — is synchronized in the request.
// The types of the platform itself are synchronized in the request too: their rows are written by the admin API, and a
// write must show at once. The planning reads Redis and the count of rows, not the source, so a request with nothing to
// wait for takes no lock.
func (a *App) syncWhileServing(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	plan, err := a.planSync(ctx, typeIDs)
	if err != nil {
		return nil, err
	}
	var stale, due []uuid.UUID
	structures := map[uuid.UUID]string{}
	for _, s := range plan.syncs {
		id := s.TypeResourceID
		_, storedStructure := splitDigest(plan.stored[id])
		structure := structureOf(g, id, s.Config)
		structures[id] = structure
		node, ok := g.Node(id)
		// The self-service types are written by the admin API itself: a write must be visible to the next request.
		platform := ok && node.ServiceID == model.PlatformServiceID
		switch {
		case plan.throttled[id]:
		case plan.stored[id] == "" || platform || storedStructure != structure:
			due = append(due, id)
		default:
			stale = append(stale, id)
		}
	}
	if len(stale) > 0 {
		counts, err := a.ActiveCounts(ctx, stale)
		if err != nil {
			return nil, err
		}
		kept := stale[:0]
		for _, id := range stale {
			// A state synchronized to an answer with no resources has no active rows, and it is current: the source said so.
			emptied := plan.stored[id] == syncDigest(dataDigest(nil), structures[id])
			if counts[id] > 0 || emptied {
				kept = append(kept, id)
			} else {
				due = append(due, id)
			}
		}
		stale = kept
	}
	if len(stale) > 0 {
		a.refreshInBackground(stale)
	}
	if len(due) > 0 {
		if _, err := a.syncTypesOnce(ctx, due, false, func(ctx context.Context) (map[uuid.UUID]bool, error) {
			return a.EnsureSynced(ctx, g, due, false)
		}); err != nil {
			return nil, err
		}
	}
	return plan.synced, nil
}

// refreshInBackground synchronizes the given types in the background, so that the requests that found their data
// stale are answered without waiting for the source. The type graph is read again here: the one of the request
// that started the refresh may predate an admin write. One refresh per set of types runs at a time in this process;
// across replicas the synchronization lock of the set decides, as for any synchronization.
func (a *App) refreshInBackground(typeIDs []uuid.UUID) {
	name := typesLockName(typeIDs)
	if _, running := a.refreshing.LoadOrStore(name, struct{}{}); running {
		return
	}
	if !a.Background(func(ctx context.Context) {
		defer a.refreshing.Delete(name)
		g, err := a.LoadTypeGraph(ctx)
		if err != nil {
			slog.WarnContext(ctx, "background refresh of the resource types failed", "error", err)
			return
		}
		if _, err := a.syncWithRedisLock(ctx, name, func(ctx context.Context) (map[uuid.UUID]bool, error) {
			return a.EnsureSynced(ctx, g, typeIDs, false)
		}); err != nil {
			slog.WarnContext(ctx, "background refresh of the resource types failed", "error", err)
		}
	}) {
		a.refreshing.Delete(name) // dropped: a later request starts the refresh again
	}
}

// metaJSON is the JSON text of an attribute map ({} for none).
func metaJSON(meta map[string]any) string {
	if len(meta) == 0 {
		return "{}"
	}
	raw, _ := json.Marshal(meta)
	return string(raw)
}

// applyType writes the incoming resources of one type: new rows are inserted, existing rows are updated where
// the database finds them different (name, attributes, or switched off), rows missing from the source (unless
// partial) are switched off, and the edges of the rows are diffed. The rows are not read back with their
// attributes: the comparison is the database's (jsonb equality), so only the names and the state come back.
// It collects into touched the rows whose set of ancestors may have changed.
func applyType(ctx context.Context, tx pgx.Tx, typeID uuid.UUID, incoming []IncomingResource, touchAll, partial bool, touched map[uuid.UUID]struct{}, changed *bool) error {
	before, updated, removed := len(touched), 0, 0
	type existing struct {
		id     uuid.UUID
		name   string
		active bool
	}
	rows, err := tx.Query(ctx, `SELECT id, external_id, name, active FROM resource_cache WHERE type_resource_id = $1`, typeID)
	if err != nil {
		return err
	}
	have := map[string]existing{}
	for rows.Next() {
		var e existing
		var ext string
		if err := rows.Scan(&e.id, &ext, &e.name, &e.active); err != nil {
			rows.Close()
			return err
		}
		have[ext] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ids := make(map[string]uuid.UUID, len(incoming))
	incomingSet := make(map[string]bool, len(incoming))
	var newExt, newName, newMeta []string
	var updID []uuid.UUID
	var updName, updMeta []string
	for _, r := range incoming {
		incomingSet[r.ExternalID] = true
		e, ok := have[r.ExternalID]
		if !ok {
			newExt = append(newExt, r.ExternalID)
			newName = append(newName, r.Name)
			newMeta = append(newMeta, metaJSON(r.Meta))
			continue
		}
		ids[r.ExternalID] = e.id
		if !e.active {
			touched[e.id] = struct{}{}
		}
		updID = append(updID, e.id)
		updName = append(updName, r.Name)
		updMeta = append(updMeta, metaJSON(r.Meta))
	}

	for offset := 0; offset < len(newExt); offset += insertChunkSize {
		end := min(offset+insertChunkSize, len(newExt))
		res, err := tx.Query(ctx, `
			INSERT INTO resource_cache (type_resource_id, external_id, name, meta, active)
			SELECT $1, u.external_id, u.name, u.meta::jsonb, true
			FROM unnest($2::varchar[], $3::varchar[], $4::text[]) AS u(external_id, name, meta)
			RETURNING id, external_id`, typeID, newExt[offset:end], newName[offset:end], newMeta[offset:end])
		if err != nil {
			return err
		}
		for res.Next() {
			var id uuid.UUID
			var ext string
			if err := res.Scan(&id, &ext); err != nil {
				res.Close()
				return err
			}
			ids[ext] = id
			touched[id] = struct{}{}
		}
		res.Close()
		if err := res.Err(); err != nil {
			return err
		}
	}

	// Only the rows that differ are written; the database compares the names and the attributes.
	for offset := 0; offset < len(updID); offset += insertChunkSize {
		end := min(offset+insertChunkSize, len(updID))
		res, err := tx.Query(ctx, `
			UPDATE resource_cache rc SET name = u.name, meta = u.meta::jsonb, active = true, synced_at = now()
			FROM unnest($1::uuid[], $2::varchar[], $3::text[]) AS u(id, name, meta)
			WHERE rc.id = u.id
			  AND (rc.name IS DISTINCT FROM u.name OR rc.meta IS DISTINCT FROM u.meta::jsonb OR NOT rc.active)
			RETURNING rc.id, rc.name`, updID[offset:end], updName[offset:end], updMeta[offset:end])
		if err != nil {
			return err
		}
		var renamedID []uuid.UUID
		var renamedName []string
		for res.Next() {
			var id uuid.UUID
			var name string
			if err := res.Scan(&id, &name); err != nil {
				res.Close()
				return err
			}
			renamedID = append(renamedID, id)
			renamedName = append(renamedName, name)
		}
		res.Close()
		if err := res.Err(); err != nil {
			return err
		}
		updated += len(renamedID)
		// The closure carries the descendant's name: keep it in step.
		if len(renamedID) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE resource_ancestor ra SET descendant_name = u.name
				FROM unnest($1::uuid[], $2::varchar[]) AS u(id, name)
				WHERE ra.descendant_id = u.id AND ra.descendant_name IS DISTINCT FROM u.name`, renamedID, renamedName); err != nil {
				return err
			}
		}
	}

	if !partial {
		var gone []uuid.UUID
		for ext, e := range have {
			if e.active && !incomingSet[ext] {
				gone = append(gone, e.id)
			}
		}
		if len(gone) > 0 {
			removed = len(gone)
			if _, err := tx.Exec(ctx,
				`UPDATE resource_cache SET active = false, synced_at = now() WHERE id = ANY($1)`, gone); err != nil {
				return err
			}
			for _, id := range gone {
				touched[id] = struct{}{}
			}
			// The children of a resource that disappeared are left with an unresolved parent.
			orphaned, err := tx.Query(ctx,
				`UPDATE resource_edge SET parent_id = NULL WHERE parent_id = ANY($1) RETURNING child_id`, gone)
			if err != nil {
				return err
			}
			for orphaned.Next() {
				var id uuid.UUID
				if err := orphaned.Scan(&id); err != nil {
					orphaned.Close()
					return err
				}
				touched[id] = struct{}{}
			}
			orphaned.Close()
			if err := orphaned.Err(); err != nil {
				return err
			}
		}
	}

	if err := syncEdges(ctx, tx, typeID, incoming, ids, touched); err != nil {
		return err
	}

	// Edges of other resources that were waiting for a parent of this type can be resolved now.
	resolved, err := tx.Query(ctx, `
		UPDATE resource_edge e SET parent_id = p.id
		FROM resource_cache p
		WHERE e.parent_id IS NULL AND e.parent_type_id = $1
		  AND p.type_resource_id = e.parent_type_id AND p.external_id = e.parent_external_id AND p.active
		RETURNING e.child_id`, typeID)
	if err != nil {
		return err
	}
	for resolved.Next() {
		var id uuid.UUID
		if err := resolved.Scan(&id); err != nil {
			resolved.Close()
			return err
		}
		touched[id] = struct{}{}
	}
	resolved.Close()
	if err := resolved.Err(); err != nil {
		return err
	}

	if changed != nil {
		// A row inserted, renamed, switched off or linked differently is a change of the data.
		*changed = len(newExt) > 0 || updated > 0 || removed > 0 || len(touched) > before
	}
	if touchAll {
		all, err := tx.Query(ctx, `SELECT id FROM resource_cache WHERE type_resource_id = $1`, typeID)
		if err != nil {
			return err
		}
		for all.Next() {
			var id uuid.UUID
			if err := all.Scan(&id); err != nil {
				all.Close()
				return err
			}
			touched[id] = struct{}{}
		}
		all.Close()
		return all.Err()
	}
	return nil
}

// syncEdges diffs the declared parents of the incoming resources against resource_edge.
func syncEdges(ctx context.Context, tx pgx.Tx, typeID uuid.UUID, incoming []IncomingResource, ids map[string]uuid.UUID, touched map[uuid.UUID]struct{}) error {
	type edgeKey struct {
		child      uuid.UUID
		parentType uuid.UUID
		parentExt  string
	}
	rows, err := tx.Query(ctx, `
		SELECT e.child_id, e.parent_type_id, e.parent_external_id
		FROM resource_edge e JOIN resource_cache c ON c.id = e.child_id
		WHERE c.type_resource_id = $1`, typeID)
	if err != nil {
		return err
	}
	have := map[edgeKey]bool{}
	for rows.Next() {
		var k edgeKey
		if err := rows.Scan(&k.child, &k.parentType, &k.parentExt); err != nil {
			rows.Close()
			return err
		}
		have[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	want := map[edgeKey]bool{}
	present := map[uuid.UUID]bool{}
	for _, r := range incoming {
		child, ok := ids[r.ExternalID]
		if !ok {
			continue
		}
		present[child] = true
		for _, e := range r.Edges {
			want[edgeKey{child, e.ParentType, e.ParentExternalID}] = true
		}
	}

	var delChild, delType []uuid.UUID
	var delExt []string
	for k := range have {
		// Edges of resources missing from a partial answer or switched off are left alone.
		if present[k.child] && !want[k] {
			delChild, delType, delExt = append(delChild, k.child), append(delType, k.parentType), append(delExt, k.parentExt)
			touched[k.child] = struct{}{}
		}
	}
	if len(delChild) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM resource_edge e USING unnest($1::uuid[], $2::uuid[], $3::varchar[]) AS u(child, ptype, pext)
			WHERE e.child_id = u.child AND e.parent_type_id = u.ptype AND e.parent_external_id = u.pext`,
			delChild, delType, delExt); err != nil {
			return err
		}
	}

	var addChild, addType []uuid.UUID
	var addExt []string
	for k := range want {
		if !have[k] {
			addChild, addType, addExt = append(addChild, k.child), append(addType, k.parentType), append(addExt, k.parentExt)
			touched[k.child] = struct{}{}
		}
	}
	for offset := 0; offset < len(addChild); offset += insertChunkSize {
		end := min(offset+insertChunkSize, len(addChild))
		if _, err := tx.Exec(ctx, `
			INSERT INTO resource_edge (child_id, parent_type_id, parent_external_id, parent_id)
			SELECT u.child, u.ptype, u.pext,
			       (SELECT p.id FROM resource_cache p
			        WHERE p.type_resource_id = u.ptype AND p.external_id = u.pext AND p.active)
			FROM unnest($1::uuid[], $2::uuid[], $3::varchar[]) AS u(child, ptype, pext)
			ON CONFLICT DO NOTHING`, addChild[offset:end], addType[offset:end], addExt[offset:end]); err != nil {
			return err
		}
	}
	return nil
}

// rebuildClosureSQL recomputes the ancestors of the given resources (parameter 1) bottom-up: from
// each of them along the resolved edges up to every ancestor, carrying the levels that flow along
// the route (the intersection of the level filters of the type links passed). A pair reachable over
// several routes gets the union of the routes' levels, NULL (all levels) if any route passes all.
// Only live resources and live type links count. Routes are deduplicated by UNION, which also makes
// a cycle in the data terminate.
const rebuildClosureSQL = `
	WITH RECURSIVE up(d, a, mask) AS (
		SELECT x.id, x.id, NULL::varchar[]
		FROM resource_cache x
		JOIN type_resource xt ON xt.id = x.type_resource_id AND xt.deleted_at IS NULL
		WHERE x.id = ANY($1) AND x.active
		UNION
		SELECT up.d, e.parent_id, nm.m
		FROM up
		JOIN resource_cache cur ON cur.id = up.a
		JOIN resource_edge e ON e.child_id = cur.id AND e.parent_id IS NOT NULL
		JOIN resource_cache par ON par.id = e.parent_id AND par.active
		JOIN type_resource pt ON pt.id = par.type_resource_id AND pt.deleted_at IS NULL
		JOIN type_resource_parent trp
		    ON trp.type_resource_id = cur.type_resource_id AND trp.parent_id = e.parent_type_id
		CROSS JOIN LATERAL (
		    SELECT CASE
		        WHEN up.mask IS NULL THEN trp.inherit
		        WHEN trp.inherit IS NULL THEN up.mask
		        ELSE ARRAY(SELECT m FROM unnest(up.mask) m WHERE m = ANY (trp.inherit) ORDER BY m)
		    END AS m
		) nm
		WHERE nm.m IS NULL OR cardinality(nm.m) > 0
	),
	paths AS (SELECT DISTINCT d, a, mask FROM up),
	closure AS (
		SELECT p.d, p.a,
		       CASE WHEN bool_or(p.mask IS NULL) THEN NULL
		            ELSE (SELECT array_agg(DISTINCT lv ORDER BY lv)
		                  FROM paths q, unnest(q.mask) lv WHERE q.d = p.d AND q.a = p.a)
		       END AS inherit
		FROM paths p GROUP BY p.d, p.a
	)
	INSERT INTO resource_ancestor
	    (ancestor_id, descendant_id, inherit, descendant_type_id, descendant_external_id, descendant_name)
	SELECT c.a, c.d, c.inherit, x.type_resource_id, x.external_id, x.name
	FROM closure c JOIN resource_cache x ON x.id = c.d
	ORDER BY c.a, c.d`

// rebuildClosure recomputes the ancestors of the touched resources and of everything below them.
// The rows of the affected descendants are replaced as a whole; rows ordered by ancestor keep the
// descendants of one anchor next to each other on disk.
func rebuildClosure(ctx context.Context, tx pgx.Tx, touched map[uuid.UUID]struct{}) error {
	if len(touched) == 0 {
		return nil
	}
	seed := make([]uuid.UUID, 0, len(touched))
	for id := range touched {
		seed = append(seed, id)
	}

	// Whatever hung below a touched resource has its ancestors changed too (found in the old closure).
	rows, err := tx.Query(ctx, `SELECT DISTINCT descendant_id FROM resource_ancestor WHERE ancestor_id = ANY($1)`, seed)
	if err != nil {
		return err
	}
	affected := map[uuid.UUID]struct{}{}
	for id := range touched {
		affected[id] = struct{}{}
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		affected[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ids := make([]uuid.UUID, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_ancestor WHERE descendant_id = ANY($1)`, ids); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, rebuildClosureSQL, ids)
	return err
}

// ---------- queries over the graph ----------

// anchorRef names a resource of a type.
type anchorRef struct {
	Type uuid.UUID
	Ext  string
}

// anchorRowIDs resolves resources (type, external id) to their active cache rows.
func (a *App) anchorRowIDs(ctx context.Context, refs []anchorRef) (map[anchorRef]uuid.UUID, error) {
	out := map[anchorRef]uuid.UUID{}
	if len(refs) == 0 {
		return out, nil
	}
	types := make([]uuid.UUID, len(refs))
	exts := make([]string, len(refs))
	for i, r := range refs {
		types[i], exts[i] = r.Type, r.Ext
	}
	rows, err := a.DB.Query(ctx, `
		SELECT c.type_resource_id, c.external_id, c.id
		FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext)
		JOIN resource_cache c ON c.type_resource_id = q.type_id AND c.external_id = q.ext AND c.active`, types, exts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r anchorRef
		var id uuid.UUID
		if err := rows.Scan(&r.Type, &r.Ext, &id); err != nil {
			return nil, err
		}
		out[r] = id
	}
	return out, rows.Err()
}

// ancestorLookup asks for the resources of type Lookup that match Value (by name, by id, or by an attribute,
// see model.MatchBy*), and for their ancestors of type Target — the resource itself when the types are the
// same. Index is the rule the lookup is for.
type ancestorLookup struct {
	Index   int
	Lookup  uuid.UUID
	Target  uuid.UUID
	Value   string
	MatchBy string
}

// findAncestorsBatch answers all lookups of one request: one query per kind of match (by name, by external id,
// by attribute), each of which keeps its own index. It returns the ancestors per lookup index, ordered by
// external id; a lookup with no match has no entry.
func (a *App) findAncestorsBatch(ctx context.Context, lookups []ancestorLookup) (map[int][]graphNode, error) {
	out := map[int][]graphNode{}
	var byName, byID, byAttr []ancestorLookup
	var attrs []string
	for _, l := range lookups {
		key, isMeta := model.MetaKeyOf(l.MatchBy)
		switch {
		case l.MatchBy == model.MatchByID:
			byID = append(byID, l)
		case isMeta:
			byAttr = append(byAttr, l)
			attrs = append(attrs, key)
		default:
			byName = append(byName, l)
		}
	}

	// run answers one kind of lookup; attr is the attribute of each lookup, for the attribute kind only.
	run := func(match string, batch []ancestorLookup, attr []string) error {
		if len(batch) == 0 {
			return nil
		}
		idx := make([]int64, len(batch))
		lookup := make([]uuid.UUID, len(batch))
		target := make([]uuid.UUID, len(batch))
		value := make([]string, len(batch))
		for i, l := range batch {
			idx[i], lookup[i], target[i], value[i] = int64(l.Index), l.Lookup, l.Target, l.Value
		}
		if attr == nil {
			attr = make([]string, len(batch))
		}
		rows, err := a.DB.Query(ctx, `
			SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name
			FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(idx, lookup_type, target_type, value, attr)
			JOIN resource_cache n ON n.type_resource_id = q.lookup_type AND n.active AND `+match+`
			JOIN resource_ancestor ra ON ra.descendant_id = n.id
			JOIN resource_cache anc ON anc.id = ra.ancestor_id AND anc.type_resource_id = q.target_type AND anc.active
			ORDER BY q.idx, anc.external_id`, idx, lookup, target, value, attr)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i int64
			var n graphNode
			if err := rows.Scan(&i, &n.ID, &n.Type, &n.Ext, &n.Name); err != nil {
				return err
			}
			out[int(i)] = append(out[int(i)], n)
		}
		return rows.Err()
	}
	if err := run(`n.name = q.value`, byName, nil); err != nil {
		return nil, err
	}
	if err := run(`n.external_id = q.value`, byID, nil); err != nil {
		return nil, err
	}
	// An attribute equal to the value or a list containing it (both forms use the GIN index).
	if err := run(`(n.meta @> jsonb_build_object(q.attr, q.value)
		OR n.meta @> jsonb_build_object(q.attr, jsonb_build_array(q.value)))`, byAttr, attrs); err != nil {
		return nil, err
	}
	return out, nil
}

// graphNode is a resource with the data the response needs.
type graphNode struct {
	ID   uuid.UUID
	Type uuid.UUID
	Ext  string
	Name string
}

// CoveredNode is a descendant of an anchor with the levels that flow to it.
type CoveredNode struct {
	graphNode
	Mask LevelSet
}

// ExpandAnchors returns, for every anchor row, the anchor itself and all its descendants of the given
// types with the levels that flow to them — one index range scan of the closure per anchor. The
// result is indexed like anchors.
func (a *App) ExpandAnchors(ctx context.Context, anchors []uuid.UUID, resultTypes []uuid.UUID) ([][]CoveredNode, error) {
	out := make([][]CoveredNode, len(anchors))
	if len(anchors) == 0 || len(resultTypes) == 0 {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `
		SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit
		FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord)
		JOIN resource_ancestor w ON w.ancestor_id = q.id
		WHERE w.descendant_type_id = ANY($2)`, anchors, resultTypes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ord int64
		var n CoveredNode
		var mask []string
		if err := rows.Scan(&ord, &n.ID, &n.Type, &n.Ext, &n.Name, &mask); err != nil {
			return nil, err
		}
		n.Mask = levelsFromDB(mask)
		out[ord-1] = append(out[ord-1], n)
	}
	return out, rows.Err()
}

// listCachedResources returns every active resource of the given types (a grant on the whole type).
func (a *App) listCachedResources(ctx context.Context, typeIDs []uuid.UUID) (map[uuid.UUID][]graphNode, error) {
	out := map[uuid.UUID][]graphNode{}
	if len(typeIDs) == 0 {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `
		SELECT id, type_resource_id, external_id, name FROM resource_cache
		WHERE type_resource_id = ANY($1) AND active`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n graphNode
		if err := rows.Scan(&n.ID, &n.Type, &n.Ext, &n.Name); err != nil {
			return nil, err
		}
		out[n.Type] = append(out[n.Type], n)
	}
	return out, rows.Err()
}

// pairMasks tells, for pairs (ancestor row, descendant row) with different rows, whether the first
// is an ancestor of the second and which levels flow between them. Missing pairs are not related.
func (a *App) pairMasks(ctx context.Context, pairs [][2]uuid.UUID) (map[[2]uuid.UUID]LevelSet, error) {
	out := map[[2]uuid.UUID]LevelSet{}
	if len(pairs) == 0 {
		return out, nil
	}
	anc := make([]uuid.UUID, len(pairs))
	desc := make([]uuid.UUID, len(pairs))
	for i, p := range pairs {
		anc[i], desc[i] = p[0], p[1]
	}
	rows, err := a.DB.Query(ctx, `
		SELECT q.a, q.d, ra.inherit
		FROM unnest($1::uuid[], $2::uuid[]) AS q(a, d)
		JOIN resource_ancestor ra ON ra.ancestor_id = q.a AND ra.descendant_id = q.d
		WHERE q.a <> q.d`, anc, desc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k [2]uuid.UUID
		var mask []string
		if err := rows.Scan(&k[0], &k[1], &mask); err != nil {
			return nil, err
		}
		out[k] = levelsFromDB(mask)
	}
	return out, rows.Err()
}

// AncestorMasks returns, for each of the given resource rows, its ancestors (itself included) with the levels
// that flow from them: what the single checks need, read in one query for all the rows.
func (a *App) AncestorMasks(ctx context.Context, nodes []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]LevelSet, error) {
	out := map[uuid.UUID]map[uuid.UUID]LevelSet{}
	if len(nodes) == 0 {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `SELECT descendant_id, ancestor_id, inherit FROM resource_ancestor WHERE descendant_id = ANY($1)`, nodes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node, ancestor uuid.UUID
		var mask []string
		if err := rows.Scan(&node, &ancestor, &mask); err != nil {
			return nil, err
		}
		if out[node] == nil {
			out[node] = map[uuid.UUID]LevelSet{}
		}
		out[node][ancestor] = levelsFromDB(mask)
	}
	return out, rows.Err()
}

// UnresolvedParents lists, for the resources of the given types that have declared parents missing
// from the cache, the parent types of those unresolved links. Such resources are "incomplete": the
// hierarchy above them is not fully known, so denials fail closed for them.
func (a *App) UnresolvedParents(ctx context.Context, typeIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	if len(typeIDs) == 0 {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `
		SELECT e.child_id, e.parent_type_id
		FROM resource_edge e JOIN resource_cache c ON c.id = e.child_id AND c.active
		WHERE e.parent_id IS NULL AND c.type_resource_id = ANY($1)`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var child, parentType uuid.UUID
		if err := rows.Scan(&child, &parentType); err != nil {
			return nil, err
		}
		out[child] = append(out[child], parentType)
	}
	return out, rows.Err()
}

// ---------- single resources (admin API) ----------

// cachedResource is a resource read from the cache.
type cachedResource struct {
	ID         uuid.UUID
	ExternalID string
	Name       string
	Parents    []model.ResourceParent
	Meta       map[string]any
	// Active is false for a resource its source no longer reports (only ListResources can return those).
	Active bool
}

// parentLabels gives, for the live parent types of a type, the label a source uses to name them:
// the type name, or service/name where the name alone is ambiguous.
func parentLabels(g *TypeGraph, typeID uuid.UUID) map[uuid.UUID]string {
	parents := g.liveParents(typeID)
	count := map[string]int{}
	for _, e := range parents {
		if n, ok := g.Node(e.Parent); ok {
			count[n.Name]++
		}
	}
	out := map[uuid.UUID]string{}
	for _, e := range parents {
		if n, ok := g.Node(e.Parent); ok {
			out[e.Parent] = n.Name
			if count[n.Name] > 1 {
				out[e.Parent] = n.ServiceName + "/" + n.Name
			}
		}
	}
	return out
}

// resourceParents reads the declared parents of the given resource rows.
func (a *App) resourceParents(ctx context.Context, g *TypeGraph, typeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]model.ResourceParent, error) {
	out := map[uuid.UUID][]model.ResourceParent{}
	if len(ids) == 0 {
		return out, nil
	}
	labels := parentLabels(g, typeID)
	rows, err := a.DB.Query(ctx, `
		SELECT child_id, parent_type_id, parent_external_id, parent_id IS NOT NULL FROM resource_edge
		WHERE child_id = ANY($1) ORDER BY parent_type_id, parent_external_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var child, parentType uuid.UUID
		var ext string
		var resolved bool
		if err := rows.Scan(&child, &parentType, &ext, &resolved); err != nil {
			return nil, err
		}
		label, ok := labels[parentType]
		if !ok {
			if n, found := g.Node(parentType); found {
				label = n.Name
			} else {
				label = parentType.String()
			}
		}
		out[child] = append(out[child], model.ResourceParent{Type: label, ID: ext, Resolved: &resolved})
	}
	return out, rows.Err()
}

func (a *App) CacheLookup(ctx context.Context, g *TypeGraph, typeResourceID uuid.UUID, externalID string) (*cachedResource, error) {
	var r cachedResource
	err := a.DB.QueryRow(ctx,
		`SELECT id, external_id, name, meta FROM resource_cache
		 WHERE type_resource_id = $1 AND external_id = $2 AND active`, typeResourceID, externalID,
	).Scan(&r.ID, &r.ExternalID, &r.Name, &r.Meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Active = true
	parents, err := a.resourceParents(ctx, g, typeResourceID, []uuid.UUID{r.ID})
	if err != nil {
		return nil, err
	}
	r.Parents = parents[r.ID]
	return &r, nil
}

// resource converts a cached resource to the API model: parent_id is set for a single parent.
func (r *cachedResource) resource() *model.Resource {
	active := r.Active
	out := &model.Resource{ID: r.ExternalID, Name: r.Name, Parents: r.Parents, Meta: r.Meta, Active: &active}
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	if out.Parents == nil {
		out.Parents = []model.ResourceParent{}
	}
	if len(r.Parents) == 1 {
		id := r.Parents[0].ID
		out.ParentID = &id
	}
	return out
}

// resolve returns a resource by its external id: a live request to the source (refreshing the cache), then a
// cache read. If the source is unavailable, the last cached value is returned; a resource switched off by the
// synchronization is not returned.
func (a *App) resolve(ctx context.Context, typeResourceID uuid.UUID, externalID string) (*cachedResource, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	sync, err := a.ActiveSynchronizer(ctx, typeResourceID)
	if err != nil {
		return nil, err
	}
	if sync != nil {
		if row := a.Sync.FetchOne(ctx, sync.Config, externalID, sync.ID.String()); row != nil {
			normalized, _ := NormalizeResources(g, typeResourceID, []model.Resource{*row})
			changed := false
			err := db.InTx(ctx, a.DB, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, syncWriteLockKey); err != nil {
					return err
				}
				touched := map[uuid.UUID]struct{}{}
				if err := applyType(ctx, tx, typeResourceID, normalized, false, true, touched, &changed); err != nil {
					return err
				}
				return rebuildClosure(ctx, tx, touched)
			})
			if err != nil {
				return nil, err
			}
			if changed {
				pipe := a.Redis.Pipeline()
				recordDataChange(ctx, pipe, g, []uuid.UUID{typeResourceID})
				if _, err := pipe.Exec(ctx); err != nil {
					slog.ErrorContext(ctx, "failed to record the resource data change", "error", err)
				}
				a.invalidateView()
			}
		}
	}
	return a.CacheLookup(ctx, g, typeResourceID, externalID)
}

// GetResource — GET …/type_resource/{id}/resource?resource_id=.
func (a *App) GetResource(ctx context.Context, typeResourceID uuid.UUID, externalID string) (*model.Resource, error) {
	r, err := a.resolve(ctx, typeResourceID, externalID)
	if err != nil || r == nil {
		return nil, err
	}
	return r.resource(), nil
}

// IsCovered: a grant on grantedID covers the resource if it is that resource or one of its
// ancestors (of any type) in the hierarchy.
func (a *App) IsCovered(ctx context.Context, typeResourceID uuid.UUID, resourceID, grantedID string) (bool, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return false, err
	}
	if _, err := a.EnsureSynced(ctx, g, []uuid.UUID{typeResourceID}, false); err != nil {
		return false, err
	}
	return Exists(ctx, a.DB, `
		SELECT 1
		FROM resource_cache d
		JOIN resource_ancestor ra ON ra.descendant_id = d.id
		JOIN resource_cache anc ON anc.id = ra.ancestor_id AND anc.active
		WHERE d.type_resource_id = $1 AND d.external_id = $2 AND d.active AND anc.external_id = $3`,
		typeResourceID, resourceID, grantedID)
}

// ListResources returns every resource of a type, synchronized from its active synchronizer and
// sorted by name: for the UI picker of a specific resource_id instead of typing ids blindly. Empty
// for a type without a synchronizer. Resources the source no longer reports are kept switched off; they are
// returned (with active=false) only with includeInactive.
func (a *App) ListResources(ctx context.Context, typeResourceID uuid.UUID, includeInactive bool) ([]model.Resource, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	// The types above are brought up to date too, so that the links of the resources to their parents are resolved
	// where the parents exist: a link is reported unresolved only when its parent is really not there.
	typeIDs := []uuid.UUID{typeResourceID}
	for id := range g.AncestorTypes(typeResourceID) {
		typeIDs = append(typeIDs, id)
	}
	synced, err := a.EnsureSynced(ctx, g, typeIDs, false)
	if err != nil {
		return nil, err
	}
	out := []model.Resource{}
	if !synced[typeResourceID] {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `
		SELECT id, external_id, name, meta, active FROM resource_cache
		WHERE type_resource_id = $1 AND ($2 OR active) ORDER BY name, external_id`, typeResourceID, includeInactive)
	if err != nil {
		return nil, err
	}
	var cached []*cachedResource
	var ids []uuid.UUID
	for rows.Next() {
		var r cachedResource
		if err := rows.Scan(&r.ID, &r.ExternalID, &r.Name, &r.Meta, &r.Active); err != nil {
			rows.Close()
			return nil, err
		}
		cached = append(cached, &r)
		ids = append(ids, r.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	parents, err := a.resourceParents(ctx, g, typeResourceID, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range cached {
		r.Parents = parents[r.ID]
		out = append(out, *r.resource())
	}
	return out, nil
}

// RefreshResources asks the source of a type now and brings the local copy up to date: the cached source list, the
// throttle and the background refresh are skipped (consistency "higher"). A type without a source cannot be refreshed;
// a source that does not answer is an error and the data stays as it was.
func (a *App) RefreshResources(ctx context.Context, typeResourceID uuid.UUID) (*model.ResourceRefresh, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := g.Node(typeResourceID); !ok {
		return nil, apperr.NotFound(i18n.TypeResourceNotFound, typeResourceID)
	}
	out, err := a.syncTypes(ctx, g, []uuid.UUID{typeResourceID}, true)
	if err != nil {
		return nil, err
	}
	if !out.synced[typeResourceID] {
		return nil, apperr.Unprocessable(i18n.SynchronizerNoSource, typeResourceID)
	}
	if out.unanswered[typeResourceID] {
		return nil, apperr.FailedDependency(i18n.SynchronizerNoAnswer, typeResourceID)
	}
	var res model.ResourceRefresh
	err = a.DB.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE NOT active)
		FROM resource_cache WHERE type_resource_id = $1`, typeResourceID).Scan(&res.Resources, &res.Inactive)
	if err != nil {
		return nil, err
	}
	return &res, nil
}
