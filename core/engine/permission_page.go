package engine

import (
	"slices"
	"sort"
	"strings"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// PermissionFilter narrows a /permission answer. Type keeps one resource type. Access keeps the resources (and the
// types as a whole) that have that access level. Limit and After page the resources of one type: they are sorted by
// id, After is the id the previous page ended with (the page starts after it), and a page that is not the last names
// the next one (NextAfter). Paging needs a type.
type PermissionFilter struct {
	Type   string
	Access string
	Limit  int
	After  string
}

// Validate reports a filter that cannot be applied.
func (f PermissionFilter) Validate() error {
	if (f.Limit > 0 || f.After != "") && f.Type == "" {
		return apperr.Unprocessable(i18n.PermissionPageNeedsType)
	}
	return nil
}

// Filter applies the filter to a result. The result itself is not changed.
func (r PermissionResult) Filter(f PermissionFilter) (PermissionResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	out := PermissionResult{}
	for name, tp := range r {
		if f.Type != "" && name != f.Type {
			continue
		}
		if tp.Type == model.TypeModeNotSync {
			if f.Access == "" || slices.Contains(tp.Access, f.Access) {
				out[name] = tp
			}
			continue
		}
		page := pageResources(tp, f)
		if f.Access != "" && len(page.Resources) == 0 {
			continue
		}
		out[name] = page
	}
	return out, nil
}

// pageResources keeps the resources of a sync type that match the filter; when the filter pages, they are sorted by id
// and cut to one page.
func pageResources(tp model.TypePermission, f PermissionFilter) model.TypePermission {
	matched := make([]model.ResourceAccess, 0, len(tp.Resources))
	for _, res := range tp.Resources {
		if f.Access != "" && !slices.Contains(res.Access, f.Access) {
			continue
		}
		matched = append(matched, res)
	}
	paged := f.Limit > 0 || f.After != ""
	if paged {
		slices.SortFunc(matched, func(a, b model.ResourceAccess) int { return strings.Compare(a.ID, b.ID) })
		if f.After != "" {
			start := sort.Search(len(matched), func(i int) bool { return matched[i].ID > f.After })
			matched = matched[start:]
		}
		if f.Limit > 0 && len(matched) > f.Limit {
			tp.NextAfter = matched[f.Limit-1].ID
			matched = matched[:f.Limit]
		}
	}
	tp.Resources = matched
	return tp
}
