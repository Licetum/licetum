package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// structureOf fingerprints what the stored data of a type depends on besides its source: the type graph above it
// (the closure is built from it) and the synchronizer configuration. A state stored under another structure is not
// current, so the type has no usable data until the worker has synchronized it again (see ensure).
func structureOf(g *TypeGraph, typeID uuid.UUID, cfg model.SynchronizerConfig) string {
	// cache_ttl only sets how long the list of the source stays in Redis: it does not change the data.
	cfg = cfg.Masked()
	cfg.CacheTTL = nil
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	return g.signature(typeID) + "-" + hex.EncodeToString(sum[:8])
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
//
// A route does not lead through a resource of a type without a live synchronizer (parameter 2,
// typesWithoutResources): such a type has no resources, so the link to the row its deleted
// synchronizer left is a link to a parent that is not there (UnresolvedParents reports it). The rows
// of such a type keep their own ancestors, ready for the day the type has a synchronizer again.
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
		JOIN resource_cache par ON par.id = e.parent_id AND par.active AND par.type_resource_id <> ALL ($2::uuid[])
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

// typesWithoutResources returns the live types that have no live synchronizer, sorted, as tx sees them: no resource of such
// a type leads anywhere in the closure (rebuildClosureSQL). It is read in the transaction that writes the closure, under
// the lock of the component of the types it writes, and not taken from a type graph loaded before: a synchronizer deleted
// or created since would otherwise be undone by the rows this transaction writes. It is never nil: the list is a
// parameter of a query (x <> ALL), and a nil slice would be sent as NULL, which nothing is unequal to.
func typesWithoutResources(ctx context.Context, tx db.Querier) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT t.id FROM type_resource t WHERE t.deleted_at IS NULL AND NOT `+
		LiveSynchronizerExists("t.id")+` ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// rebuildClosure recomputes the ancestors of the touched resources and of everything below them.
// The rows of the affected descendants are replaced as a whole; rows ordered by ancestor keep the
// descendants of one anchor next to each other on disk. The types that have resources are the ones tx sees
// (typesWithoutResources).
func rebuildClosure(ctx context.Context, tx db.Querier, touched map[uuid.UUID]struct{}) error {
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
	without, err := typesWithoutResources(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, rebuildClosureSQL, ids, without)
	return err
}

// ---------- queries over the graph ----------

// anchorRef names a resource of a type.
type anchorRef struct {
	Type uuid.UUID
	Ext  string
}

// anchorRowIDs resolves resources (type, external id) to their active cache rows. The callers ask only for resources of
// types that have resources (TypeGraph.IsSync): the rows a deleted synchronizer left are not resources to decide.
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
// see model.MatchBy*), and for their ancestors of type Target through the resource links, whatever the inherit
// masks along the way — the resource itself when the types are the same. Index is the rule the lookup is for.
// Between is TypeGraph.TypesBetween(Lookup, Target): a climb that leaves these types cannot come back to the target.
type ancestorLookup struct {
	Index   int
	Lookup  uuid.UUID
	Target  uuid.UUID
	Value   string
	MatchBy string
	Between []uuid.UUID
}

// ancestorWalkSQL finds the anchors of the lookups of one kind of match (match is the condition on the matched resource
// n and the lookup q). The ancestors are found by walking the resolved edges up, not through resource_ancestor: the
// closure keeps only the pairs some level flows between (rebuildClosureSQL drops a route whose masks intersect to
// nothing), while the masks say what flows down a link, not which resource lies above which. Dropping such an ancestor
// would drop the anchor of a denial (fail open). The walk takes the edges the closure takes: resolved, to a live
// resource of a live type, along a live type link; UNION makes a cycle in the data terminate.
//
// The walk climbs no further than an anchor can be:
//   - It stops at a resource of the target type. The type graph has no cycle (checkParents refuses a type as its own
//     parent, as ck_type_resource_parent_no_self does, and a link that closes a cycle), so no resource above one of the
//     target type is of the target type. With the lookup type as the target (the owner pattern) nothing is climbed: the
//     matched resources are the anchors.
//   - It follows a link only to a parent whose type lies on a route of type links up to the target (q.allowed, the live
//     types of Between): above any other type the target cannot be. A resolved edge points at a resource of its parent
//     type (syncEdges and the resolution of the edges waiting for a parent match on it), so the type of the link is the
//     type of the parent, and only q.allowed has to be live.
//
// q is made once per lookup: the live types of Between, and the objects the attribute match looks for in meta (the value,
// or a list holding it), which cost more than the match itself when they are built for every resource. A row of the walk
// carries the type, the external id and the name of its resource, so the answer needs no join.
func ancestorWalkSQL(match string) string {
	return `
		WITH RECURSIVE q AS MATERIALIZED (
			SELECT u.idx, u.lookup_type, u.target_type, u.value,
			       jsonb_build_object(u.attr, u.value) AS meta_value,
			       jsonb_build_object(u.attr, jsonb_build_array(u.value)) AS meta_list,
			       ARRAY(SELECT t.id FROM type_resource t WHERE t.id = ANY (u.allowed::uuid[]) AND t.deleted_at IS NULL) AS allowed
			FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[], $6::text[])
			    AS u(idx, lookup_type, target_type, value, attr, allowed)
		),
		up(idx, target_type, allowed, id, type_id, ext, name) AS (
			SELECT q.idx, q.target_type, q.allowed, n.id, n.type_resource_id, n.external_id, n.name
			FROM q
			JOIN resource_cache n ON n.type_resource_id = q.lookup_type AND n.active AND ` + match + `
			JOIN type_resource nt ON nt.id = n.type_resource_id AND nt.deleted_at IS NULL
			UNION
			SELECT up.idx, up.target_type, up.allowed, par.id, par.type_resource_id, par.external_id, par.name
			FROM up
			JOIN type_resource_parent trp ON trp.type_resource_id = up.type_id AND trp.parent_id = ANY (up.allowed)
			JOIN resource_edge e ON e.child_id = up.id AND e.parent_type_id = trp.parent_id AND e.parent_id IS NOT NULL
			JOIN resource_cache par ON par.id = e.parent_id AND par.active
			WHERE up.type_id <> up.target_type
		)
		SELECT idx, id, type_id, ext, name FROM up
		WHERE type_id = target_type
		ORDER BY idx, ext`
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
		allowed := make([]string, len(batch))
		for i, l := range batch {
			idx[i], lookup[i], target[i], value[i] = int64(l.Index), l.Lookup, l.Target, l.Value
			allowed[i] = uuidArrayLiteral(l.Between)
		}
		if attr == nil {
			attr = make([]string, len(batch))
		}
		rows, err := a.DB.Query(ctx, ancestorWalkSQL(match), idx, lookup, target, value, attr, allowed)
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
	if err := run(`(n.meta @> q.meta_value OR n.meta @> q.meta_list)`, byAttr, attrs); err != nil {
		return nil, err
	}
	return out, nil
}

// uuidArrayLiteral writes ids as a PostgreSQL array literal ({a,b}): an element of a text[] parameter that the query
// casts to uuid[], which gives each row of an unnest an array of its own.
func uuidArrayLiteral(ids []uuid.UUID) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(id.String())
	}
	b.WriteByte('}')
	return b.String()
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

// UnresolvedLink is a parent link that is not resolved at or above a resource: the resource itself, or one of its
// ancestors (FromType is the type of the one that declares it), names a parent of type ParentType that is not in the cache.
// Route is the set of levels that flow from the one that declares it down to the resource (all levels for the resource
// itself): a denial above the missing parent could reach the resource only with those levels.
type UnresolvedLink struct {
	FromType   uuid.UUID
	ParentType uuid.UUID
	Route      LevelSet
}

// UnresolvedParents lists, for the active resources of the given types, the unresolved parent links at or above them:
// their own and those of every ancestor. Such resources are "incomplete": the hierarchy above them is not fully known,
// so denials fail closed for them — a resource below an ancestor whose parent is missing may lie under any denial that
// could cover that parent, as much as one whose own parent is missing. The query starts from the unresolved edges
// (a partial index) and walks down the closure, whose row of a resource for itself carries the link of its own edges.
//
// A link to a row of a type above that lost its synchronizer (g says which) is unresolved too, though the edge points at
// the row: that type has no resources, so the parent is not there (the closure does not lead through it either, see
// rebuildClosureSQL). Those links are read apart, from the rows of such types, so that the usual query keeps its plan.
func (a *App) UnresolvedParents(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID) (map[uuid.UUID][]UnresolvedLink, error) {
	out := map[uuid.UUID][]UnresolvedLink{}
	if len(typeIDs) == 0 {
		return out, nil
	}
	links, args := `SELECT child_id, parent_type_id FROM resource_edge WHERE parent_id IS NULL`, []any{typeIDs}
	// A type that never had a synchronizer has no rows: the links to it are never resolved in the first place.
	var left []uuid.UUID
	if len(g.sourceLost) > 0 {
		above := g.AncestorTypes(typeIDs...)
		for _, id := range g.sourceLost {
			if above[id] {
				left = append(left, id)
			}
		}
	}
	if len(left) > 0 {
		links += ` UNION ALL SELECT e.child_id, e.parent_type_id FROM resource_edge e JOIN resource_cache p ON p.id = e.parent_id
			WHERE p.type_resource_id = ANY($2)`
		args = append(args, left)
	}
	rows, err := a.DB.Query(ctx, `
		SELECT anc.descendant_id, x.type_resource_id, e.parent_type_id, anc.inherit
		FROM (`+links+`) e
		JOIN resource_cache x ON x.id = e.child_id AND x.active
		JOIN resource_ancestor anc ON anc.ancestor_id = e.child_id
		JOIN resource_cache d ON d.id = anc.descendant_id AND d.active
		WHERE anc.descendant_type_id = ANY($1)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var node, fromType, parentType uuid.UUID
		var route []string
		if err := rows.Scan(&node, &fromType, &parentType, &route); err != nil {
			return nil, err
		}
		link := UnresolvedLink{FromType: fromType, ParentType: parentType, Route: levelsFromDB(route)}
		// Several missing parents of one type, at one place, are one link.
		key := fromType.String() + parentType.String() + link.Route.key()
		if seen[node] == nil {
			seen[node] = map[string]bool{}
		}
		if seen[node][key] {
			continue
		}
		seen[node][key] = true
		out[node] = append(out[node], link)
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

// resourceParents reads the declared parents of the given resource rows. A link to a parent of a type without a live
// synchronizer is not resolved, though the edge points at the row its deleted synchronizer left: that type has no
// resources (TypeGraph.IsSync), as the decisions read it (UnresolvedParents).
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
		resolved = resolved && g.IsSync(parentType)
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

// CacheLookup reads one resource of a type from the local copy: nil when the type has no such active resource, and nil
// for any resource of a type without a live synchronizer, which has none (see TypeGraph.IsSync).
func (a *App) CacheLookup(ctx context.Context, g *TypeGraph, typeResourceID uuid.UUID, externalID string) (*cachedResource, error) {
	if !g.IsSync(typeResourceID) {
		return nil, nil
	}
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
// synchronization is not returned. A source that did not answer the synchronization lately (the type is in backoff in
// sync_state, for every replica) is not asked: the cached value is returned at once.
func (a *App) resolve(ctx context.Context, typeResourceID uuid.UUID, externalID string) (*cachedResource, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	if a.Role == RolePDP {
		return nil, errors.New("the pdp role does not write the resource graph")
	}
	sync, err := a.ActiveSynchronizer(ctx, typeResourceID)
	if err != nil {
		return nil, err
	}
	if sync != nil {
		states, err := loadSyncStates(ctx, a.DB, []uuid.UUID{typeResourceID})
		if err != nil {
			return nil, err
		}
		if states[typeResourceID].Backoff {
			return a.CacheLookup(ctx, g, typeResourceID, externalID)
		}
		if row := a.Sync.FetchOne(ctx, sync.Config, externalID, sync.ID.String()); row != nil {
			normalized, _ := NormalizeResources(g, typeResourceID, []model.Resource{*row})
			changed := false
			// The inline write takes the lock of the component of the type, like a synchronization does, and has the same
			// timeouts.
			err := a.syncTx(ctx, ComponentKeys(g, []uuid.UUID{typeResourceID}), func(tx pgx.Tx) error {
				touched := map[uuid.UUID]struct{}{}
				if err := applyType(ctx, tx, typeResourceID, normalized, false, true, touched, &changed); err != nil {
					return err
				}
				if err := rebuildClosure(ctx, tx, touched); err != nil {
					return err
				}
				if changed {
					return bumpDataRevisions(ctx, tx, g, []uuid.UUID{typeResourceID})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if changed {
				a.invalidateView()
				pipe := a.Redis.Pipeline()
				recordDataChange(ctx, pipe, g, []uuid.UUID{typeResourceID})
				if _, err := pipe.Exec(ctx); err != nil {
					logRedis(ctx, slog.LevelWarn, "failed to record the resource data change for the previous release", err)
				}
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
// ancestors (of any type) in the hierarchy. Neither is there when its type has no live synchronizer.
func (a *App) IsCovered(ctx context.Context, typeResourceID uuid.UUID, resourceID, grantedID string) (bool, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return false, err
	}
	if !g.IsSync(typeResourceID) {
		return false, nil
	}
	if _, err := a.EnsureSynced(ctx, g, []uuid.UUID{typeResourceID}, false); err != nil {
		return false, err
	}
	return Exists(ctx, a.DB, `
		SELECT 1
		FROM resource_cache d
		JOIN resource_ancestor ra ON ra.descendant_id = d.id
		JOIN resource_cache anc ON anc.id = ra.ancestor_id AND anc.active AND anc.type_resource_id <> ALL ($4::uuid[])
		WHERE d.type_resource_id = $1 AND d.external_id = $2 AND d.active AND anc.external_id = $3`,
		typeResourceID, resourceID, grantedID, g.withoutResources())
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
	out, err := a.ensure(ctx, g, []uuid.UUID{typeResourceID}, SyncOptions{Fresh: true, Wait: a.adminWait()}, "admin")
	if err != nil {
		return nil, err
	}
	if !out.Synced[typeResourceID] {
		return nil, apperr.Unprocessable(i18n.SynchronizerNoSource, typeResourceID)
	}
	if out.Unanswered[typeResourceID] || out.Pending {
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
