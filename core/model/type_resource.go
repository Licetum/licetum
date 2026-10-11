package model

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// AllLevels is the wildcard access level of a denial: "everything".
const AllLevels = "*"

// TypeParent is a link from a resource type to one of its parent types. Resources of the type
// inherit access from resources of the parent type along the link.
type TypeParent struct {
	_              struct{}  `additionalProperties:"true"`
	TypeResourceID uuid.UUID `json:"type_resource_id" format:"uuid" doc:"Parent resource type id (may belong to another service)"`
	Inherit        []string  `json:"inherit" required:"false" nullable:"true" doc:"Access levels that flow down along this link, both grants and denials; null or omitted means all levels" example:"[\"read\"]"`
}

// TypeResourceInput is the body of POST/PATCH /service/{id}/type_resource/ (PATCH replaces everything).
type TypeResourceInput struct {
	_                    struct{}            `additionalProperties:"true"`
	Name                 string              `json:"name" doc:"Resource type name" example:"branch"`
	Parents              []TypeParent        `json:"parents" required:"false" doc:"Parent resource types. Access granted on a resource of a parent type cascades to the resources of this type linked to it. Takes precedence over type_resource_parent_id"`
	TypeResourceParentID *uuid.UUID          `json:"type_resource_parent_id" format:"uuid" required:"false" deprecated:"true" doc:"Deprecated shortcut for a single parent with all levels inherited; used only when parents is omitted"`
	AccessImplies        map[string][]string `json:"access_implies" required:"false" doc:"Access level hierarchy of this type: {\"write\": [\"read\"]} means write includes read (and a denial of read also denies write)" example:"{\"write\":[\"read\"]}"`
}

// Validate normalizes the input (the deprecated single parent becomes the parents list) and checks
// the parents and the level hierarchy.
func (t *TypeResourceInput) Validate() error {
	if t.Parents == nil && t.TypeResourceParentID != nil {
		t.Parents = []TypeParent{{TypeResourceID: *t.TypeResourceParentID}}
	}
	t.TypeResourceParentID = nil

	seen := map[uuid.UUID]bool{}
	for i, p := range t.Parents {
		if seen[p.TypeResourceID] {
			return apperr.Unprocessable(i18n.TypeParentDuplicate, p.TypeResourceID)
		}
		seen[p.TypeResourceID] = true
		for _, level := range p.Inherit {
			if level == "" || level == AllLevels {
				return apperr.Unprocessable(i18n.AccessLevelInvalid, "parents.inherit")
			}
		}
		if p.Inherit != nil {
			levels := slices.Clone(p.Inherit)
			slices.Sort(levels)
			t.Parents[i].Inherit = slices.Compact(levels)
		}
	}

	if t.AccessImplies == nil {
		t.AccessImplies = map[string][]string{}
	}
	for level, implied := range t.AccessImplies {
		if level == "" || level == AllLevels {
			return apperr.Unprocessable(i18n.AccessLevelInvalid, "access_implies")
		}
		for _, other := range implied {
			if other == "" || other == AllLevels {
				return apperr.Unprocessable(i18n.AccessLevelInvalid, "access_implies")
			}
		}
	}
	if cycle := impliesCycle(t.AccessImplies); cycle != "" {
		return apperr.Unprocessable(i18n.AccessImpliesCycle, cycle)
	}
	return nil
}

// impliesCycle returns a level that (transitively) implies itself, or "" if the hierarchy is acyclic.
func impliesCycle(implies map[string][]string) string {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(level string) string
	visit = func(level string) string {
		switch state[level] {
		case visiting:
			return level
		case done:
			return ""
		}
		state[level] = visiting
		for _, next := range implies[level] {
			if c := visit(next); c != "" {
				return c
			}
		}
		state[level] = done
		return ""
	}
	for _, level := range slices.Sorted(maps.Keys(implies)) {
		if c := visit(level); c != "" {
			return c
		}
	}
	return ""
}

// ImpliedClosure returns, for every level, the levels it includes (itself excluded), transitively.
func ImpliedClosure(implies map[string][]string) map[string][]string {
	out := make(map[string][]string, len(implies))
	for level := range implies {
		seen := map[string]bool{level: true}
		queue := slices.Clone(implies[level])
		var all []string
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			if seen[next] {
				continue
			}
			seen[next] = true
			all = append(all, next)
			queue = append(queue, implies[next]...)
		}
		slices.Sort(all)
		out[level] = all
	}
	return out
}

// TypeResource is a row of the type_resource table (soft-deleted rows excluded).
type TypeResource struct {
	ID                   uuid.UUID           `json:"id" format:"uuid" doc:"Resource type id"`
	ServiceID            uuid.UUID           `json:"service_id" format:"uuid" doc:"Service id"`
	Name                 string              `json:"name" doc:"Resource type name"`
	Parents              []TypeParent        `json:"parents" nullable:"false" doc:"Parent resource types"`
	TypeResourceParentID *uuid.UUID          `json:"type_resource_parent_id" format:"uuid" deprecated:"true" doc:"Deprecated: the parent id when the type has exactly one parent, otherwise null"`
	AccessImplies        map[string][]string `json:"access_implies" nullable:"false" doc:"Access level hierarchy of this type"`
}

// ParentTypeIDs lists the ids of the parent types.
func (t TypeResource) ParentTypeIDs() []uuid.UUID {
	out := make([]uuid.UUID, len(t.Parents))
	for i, p := range t.Parents {
		out[i] = p.TypeResourceID
	}
	return out
}

type TypeResourceWithService struct {
	TypeResource
	ServiceName string `json:"service_name" doc:"Name of the owning service"`
}

// ResourceParent is a declared parent of a resource: the parent type (its name, or service/name when
// the name is ambiguous among the parent types of the resource type) and the parent id.
type ResourceParent struct {
	Type string `json:"type" doc:"Name of the parent resource type; service/name when the name alone is ambiguous" example:"branch"`
	ID   string `json:"id" doc:"Parent resource id in the external system" example:"b1"`
	// Resolved is set in the answers of the admin API only (never by a source).
	Resolved *bool `json:"resolved,omitempty" required:"false" doc:"false while the parent is not in the cache yet (an unresolved link): access under any denial that could cover the resource is then refused; the link resolves when the parent appears"`
}

// Resource is a resource from a synchronizer or the cache.
type Resource struct {
	ID       string           `json:"id" doc:"Resource id in the external system" example:"b1"`
	Name     string           `json:"name" doc:"Resource name" example:"Branch1"`
	ParentID *string          `json:"parent_id" doc:"Parent resource id — for a resource type with a single parent type" example:"d1"`
	Parents  []ResourceParent `json:"parents" required:"false" nullable:"false" doc:"Parents of the resource (a resource may have several, of different types)"`
	Meta     map[string]any   `json:"meta" required:"false" nullable:"false" doc:"Attributes the source reported besides id, name and the parents: strings or arrays of strings. Rules can match a claim against them (match_by meta.<name>)"`
	// Active is set in the answers of the admin API only (never by a source).
	Active *bool `json:"active,omitempty" required:"false" doc:"false for a resource that disappeared from its source: it is kept, switched off, and listed only with include_inactive"`
}

// ResourceAccess is an item of the /permission response.
type ResourceAccess struct {
	ID     string   `json:"id" doc:"Resource id in the external system" example:"b1"`
	Name   string   `json:"name" doc:"Resource name" example:"Branch1"`
	Access []string `json:"access" doc:"Access levels" example:"[\"read\",\"write\"]"`
}

// ResourceRefresh is the answer of a forced refresh of a type from its source.
type ResourceRefresh struct {
	Resources int `json:"resources" doc:"Active resources of the type after the refresh"`
	Inactive  int `json:"inactive" doc:"Resources kept but switched off because the source no longer reports them"`
}

// A type's mode is derived, not stored: a type is sync when a live (not deleted) synchronizer
// points at it — it has resources, and /permission lists them — otherwise it is not_sync, a
// "permission without resources" (a capability such as advanced_working_mode): a rule with
// resource_id = NULL grants access to the type as a whole.
const (
	TypeModeSync    = "sync"
	TypeModeNotSync = "not_sync"
)

// SyncTypePermission is the /permission entry of a type with a synchronizer: access per resource.
type SyncTypePermission struct {
	Type      string           `json:"type" enum:"sync" doc:"Type mode: the type has a synchronizer, access is granted per resource"`
	Resources []ResourceAccess `json:"resources" nullable:"false" doc:"Resources of the type available to the subject, with access levels"`
	NextAfter string           `json:"next_after,omitempty" doc:"Present when the answer is a page that is not the last: pass it as after to read the next page"`
}

// NotSyncTypePermission is the /permission entry of a type without a synchronizer: access to the
// type itself.
type NotSyncTypePermission struct {
	Type   string   `json:"type" enum:"not_sync" doc:"Type mode: the type has no synchronizer (no resources), access is granted to the type as a whole"`
	Access []string `json:"access" nullable:"false" doc:"Access levels of the subject on the type" example:"[\"read\",\"write\"]"`
}

// TypePermission is an entry of the /permission response: a union discriminated by type —
// SyncTypePermission or NotSyncTypePermission.
type TypePermission struct {
	Type      string
	Resources []ResourceAccess
	Access    []string
	// NextAfter is the cursor of the next page of Resources (see service.PermissionFilter); empty on the last page.
	NextAfter string
}

func (p TypePermission) MarshalJSON() ([]byte, error) {
	if p.Type == TypeModeNotSync {
		access := p.Access
		if access == nil {
			access = []string{}
		}
		return json.Marshal(NotSyncTypePermission{Type: TypeModeNotSync, Access: access})
	}
	resources := p.Resources
	if resources == nil {
		resources = []ResourceAccess{}
	}
	return json.Marshal(SyncTypePermission{Type: TypeModeSync, Resources: resources, NextAfter: p.NextAfter})
}

func (p *TypePermission) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type      string           `json:"type"`
		Resources []ResourceAccess `json:"resources"`
		Access    []string         `json:"access"`
		NextAfter string           `json:"next_after"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = TypePermission{Type: raw.Type, Resources: raw.Resources, Access: raw.Access, NextAfter: raw.NextAfter}
	return nil
}

// Schema describes the union for OpenAPI: oneOf the two entry shapes with a discriminator on type.
func (TypePermission) Schema(r huma.Registry) *huma.Schema {
	sync := r.Schema(reflect.TypeFor[SyncTypePermission](), true, "SyncTypePermission")
	notSync := r.Schema(reflect.TypeFor[NotSyncTypePermission](), true, "NotSyncTypePermission")
	return &huma.Schema{
		Description: "Access of the subject on a resource type; the shape depends on the type mode",
		OneOf:       []*huma.Schema{sync, notSync},
		Discriminator: &huma.Discriminator{
			PropertyName: "type",
			Mapping:      map[string]string{TypeModeSync: sync.Ref, TypeModeNotSync: notSync.Ref},
		},
	}
}
