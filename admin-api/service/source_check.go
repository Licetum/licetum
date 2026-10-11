package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const (
	sourceTestSample = 10
	sourceTestListed = 20
	// sourceTestEdgeChecks bounds the parent links looked up in the cache in one test.
	sourceTestEdgeChecks = 50_000
)

// TestSource asks a source that is not saved (or an edited one) for its whole list and says what Licetum would make of
// it: how many resources, the first few, repeated ids, parents that are not known, and the attributes. Nothing is
// written. The answer of the source goes through the same normalization the synchronization uses.
func (a *App) TestSource(ctx context.Context, serviceID, typeResourceID uuid.UUID, in *model.SynchronizerTestInput) (*model.SourceTestResult, error) {
	if _, err := a.GetExistingTypeResource(ctx, a.DB, serviceID, typeResourceID); err != nil {
		return nil, err
	}
	cfg := in.Config
	if in.SynchronizerID != nil {
		existing, err := a.getExistingSynchronizer(ctx, a.DB, typeResourceID, *in.SynchronizerID)
		if err != nil {
			return nil, err
		}
		cfg = cfg.RestoreSecrets(existing.Config)
	}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	// The parent types are brought up to date, or every parent would look missing.
	if parents := engine.Keys(g.AncestorTypes(typeResourceID)); len(parents) > 0 {
		if _, err := a.EnsureSynced(ctx, g, parents, false); err != nil {
			return nil, err
		}
	}

	started := time.Now()
	rows, err := a.Sync.FetchAll(ctx, cfg, "", true)
	elapsed := time.Since(started)
	if err != nil {
		return nil, apperr.FailedDependency(i18n.SourceTestFailed, redact(err.Error(), cfg.Secrets()))
	}

	res := &model.SourceTestResult{
		Total: len(rows), Sample: []model.Resource{}, Duplicates: []string{},
		Orphans: []model.SourceOrphan{}, Attributes: []model.SourceAttribute{}, DurationMS: elapsed.Milliseconds(),
	}

	// Repeated ids: the last row wins, as in the synchronization.
	seen := make(map[string]int, len(rows))
	unique := make([]model.Resource, 0, len(rows))
	for _, r := range rows {
		if i, dup := seen[r.ID]; dup {
			unique[i] = r
			if res.DuplicateCount++; len(res.Duplicates) < sourceTestListed && !slices.Contains(res.Duplicates, r.ID) {
				res.Duplicates = append(res.Duplicates, r.ID)
			}
			continue
		}
		seen[r.ID] = len(unique)
		unique = append(unique, r)
	}

	res.Sample = append(res.Sample, unique[:min(sourceTestSample, len(unique))]...)

	attrs := map[string]int{}
	for _, r := range unique {
		for name := range r.Meta {
			attrs[name]++
		}
	}
	for name, count := range attrs {
		res.Attributes = append(res.Attributes, model.SourceAttribute{Name: name, Count: count})
	}
	slices.SortFunc(res.Attributes, func(x, y model.SourceAttribute) int {
		if x.Count != y.Count {
			return y.Count - x.Count
		}
		return strings.Compare(x.Name, y.Name)
	})
	if len(res.Attributes) > 50 {
		res.Attributes = res.Attributes[:50]
	}

	incoming, dropped := engine.NormalizeResources(g, typeResourceID, unique)
	res.Unresolvable = dropped
	if err := a.findOrphans(ctx, g, typeResourceID, incoming, seen, res); err != nil {
		return nil, err
	}
	return res, nil
}

// findOrphans records the parent links that point at nothing known: not in this answer (for a parent of the same type)
// and not in the cache of the parent type.
func (a *App) findOrphans(ctx context.Context, g *engine.TypeGraph, typeID uuid.UUID, incoming []engine.IncomingResource, inAnswer map[string]int, res *model.SourceTestResult) error {
	type link struct {
		child  string
		parent engine.EdgeRef
	}
	var links []link
	for _, r := range incoming {
		for _, e := range r.Edges {
			if e.ParentType == typeID {
				if _, ok := inAnswer[e.ParentExternalID]; ok {
					continue
				}
			}
			links = append(links, link{child: r.ExternalID, parent: e})
		}
	}
	if len(links) == 0 {
		return nil
	}
	if len(links) > sourceTestEdgeChecks {
		links = links[:sourceTestEdgeChecks]
	}
	types := make([]uuid.UUID, len(links))
	ids := make([]string, len(links))
	for i, l := range links {
		types[i], ids[i] = l.parent.ParentType, l.parent.ParentExternalID
	}
	rows, err := a.DB.Query(ctx, `
		SELECT e.t, e.x FROM unnest($1::uuid[], $2::text[]) AS e(t, x)
		WHERE EXISTS (SELECT 1 FROM resource_cache c
		              WHERE c.type_resource_id = e.t AND c.external_id = e.x AND c.active)`, types, ids)
	if err != nil {
		return err
	}
	known := map[engine.EdgeRef]bool{}
	for rows.Next() {
		var t uuid.UUID
		var x string
		if err := rows.Scan(&t, &x); err != nil {
			rows.Close()
			return err
		}
		known[engine.EdgeRef{ParentType: t, ParentExternalID: x}] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	counted := map[string]bool{}
	for _, l := range links {
		if known[l.parent] {
			continue
		}
		if !counted[l.child] {
			counted[l.child] = true
			res.OrphanCount++
		}
		if len(res.Orphans) < sourceTestListed {
			name := l.parent.ParentType.String()
			if n, ok := g.Node(l.parent.ParentType); ok {
				name = n.Name
			}
			res.Orphans = append(res.Orphans, model.SourceOrphan{ID: l.child, ParentType: name, ParentID: l.parent.ParentExternalID})
		}
	}
	return nil
}

// redact hides the secrets of a source in a message that is shown to a person.
func redact(message string, secrets []string) string {
	for _, s := range secrets {
		// A very short secret would mangle the message more than it hides.
		if len(s) >= 3 {
			message = strings.ReplaceAll(message, s, model.SecretMask)
		}
	}
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	return message
}
