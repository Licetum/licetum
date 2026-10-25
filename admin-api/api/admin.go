package api

import (
	"context"
	"net/http"

	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

var adminTags = []*huma.Tag{
	{Name: "Service", Description: "Client applications registered in Licetum. The slug of a service names it in the paths of the PDP."},
	{Name: "Type resource", Description: "Resource types of a service. The parent (type_resource_parent_id) may belong to another service and defines the permission inheritance chain."},
	{Name: "Resource", Description: "Concrete resources of a type and the synchronizers that fetch them from the owning service (SQL query or HTTP)."},
	{Name: "Group", Description: "Global groups of people: a group is a subject too (`group:<uuid>`), so it is granted permissions the same way as a user."},
	{Name: "Group member", Description: "Who belongs to a group: by a predicate over claims (the properties of the subject) or by nesting another group."},
	{Name: "Role", Description: "Global roles: named sets of permissions. A role is not a subject: an assignment rule grants it (role_id)."},
	{Name: "Role permission", Description: "The items of a role: access levels on a target of a resource type."},
	{Name: "AssignmentRule", Description: "Rules granting access to a resource type or a specific resource."},
	{Name: "Platform", Description: "Self-service: Licetum registered within itself. The admin API is protected by its policies — the resource types service, type_resource (with its synchronizers and resources), group, group_member, role and assignment_rule: listing needs read on the entities (the list is filtered), changing an entity needs write on it, creating needs write on the whole type."},
	{Name: "System", Description: "Service endpoints."},
}

// NewAdminHandler — admin API (services, type_resources, resources, groups, group_members, roles,
// assignment_rules, platform). Port 8000 by default.
func NewAdminHandler(app *service.App) http.Handler {
	h, _ := NewAdminAPI(app)
	return h
}

// NewAdminAPI is the same but also returns the huma.API (to generate OpenAPI without running a server).
func NewAdminAPI(app *service.App) (http.Handler, huma.API) {
	h := &adminHandlers{app: app}
	tags := append(append([]*huma.Tag{}, adminTags...), adminOpsTags...)
	return server.NewServer(app.App, "Licetum — admin", tags, func(api huma.API) {
		httpx.AddBearerScheme(api, "Access token issued by the IdP of the Licetum self-service (PLATFORM__*). "+
			"Permissions come from the platform policies: service, type_resource, group, group_member, role, assignment_rule.")
		api.UseMiddleware(platformAuth(app))
		h.registerServices(api)
		h.registerTypeResources(api)
		h.registerAccessGroups(api)
		h.registerResources(api)
		h.registerGroups(api)
		h.registerRoles(api)
		h.registerAssignmentRules(api)
		h.registerPlatform(api)
		h.registerOperations(api)
		h.registerBatch(api)
		h.registerPolicy(api)
		h.registerPEP(api)
		h.registerDirectory(api)
		h.registerSimulation(api)
	})
}

type adminHandlers struct {
	app *service.App
}

// ---------- path parameters ----------

type ServicePath struct {
	ServiceID uuid.UUID `path:"service_id" format:"uuid" doc:"Service id"`
}

type TypeResourcePath struct {
	ServicePath
	TypeResourceID uuid.UUID `path:"type_resource_id" format:"uuid" doc:"Resource type id"`
}

type SynchronizerPath struct {
	TypeResourcePath
	SynchronizerID uuid.UUID `path:"synchronizer_id" format:"uuid" doc:"Synchronizer id"`
}

type GroupPath struct {
	GroupID uuid.UUID `path:"group_id" format:"uuid" doc:"Group id"`
}

type GroupMemberPath struct {
	GroupPath
	MemberID uuid.UUID `path:"member_id" format:"uuid" doc:"Membership rule id"`
}

type RolePath struct {
	RoleID uuid.UUID `path:"role_id" format:"uuid" doc:"Role id"`
}

type RolePermissionPath struct {
	RolePath
	PermissionID uuid.UUID `path:"permission_id" format:"uuid" doc:"Permission item id"`
}

type RulePath struct {
	RuleID uuid.UUID `path:"rule_id" format:"uuid" doc:"Rule id"`
}

func validate(v model.Validator) error { return v.Validate() }

// ---------- Service ----------

func (h *adminHandlers) registerServices(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_service",
		Method:      http.MethodPost,
		Path:        "/service",
		Summary:     "Register a service",
		Description: "Registers a service. Its slug names it in the paths of the PDP (/service/<slug>/access/v1/...). The url and the icon are optional and are only shown in the interface: Licetum does not request them.",
		Tags:        []string{"Service"},
	}, requireWholeType(model.PlatformService, model.ActionWrite)), func(ctx context.Context, in *struct {
		Body model.ServiceInput
	}) (*server.Out[*model.ServiceResponse], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateService(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_services",
		Method:      http.MethodGet,
		Path:        "/service",
		Summary:     "List services",
		Description: "All services that are not deleted.",
		Tags:        []string{"Service"},
	}, requireAny(model.PlatformService, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.ServiceResponse], error) {
		out, err := h.app.ListServices(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformService, out, func(s model.ServiceResponse) uuid.UUID { return s.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_service",
		Method:      http.MethodPatch,
		Path:        "/service/{service_id}",
		Summary:     "Update a service",
		Description: "Partial update: the fields sent are replaced, the rest stay as they are. At least one field is required.",
		Tags:        []string{"Service"},
		Errors:      []int{http.StatusFailedDependency},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *struct {
		ServicePath
		Body model.ServiceUpdate
	}) (*server.Out[*model.ServiceResponse], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateService(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_service",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}",
		Summary:     "Delete a service",
		Description: "Soft delete. Not allowed while the service has active resource types (409, extra.blocked_by).",
		Tags:        []string{"Service"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteService(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// ---------- TypeResource ----------

func (h *adminHandlers) registerTypeResources(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_type_resource",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/type_resource",
		Summary:     "Create a resource type",
		Description: "A type may have several parents (a DAG), each of any service; `inherit` on a parent limits the access levels that flow down along that link (grants and denials alike). A grant on a resource of a parent type cascades to the resources of the child type linked to it — set parents only where such inheritance is needed. `access_implies` defines the access level hierarchy of the type. The name is unique within the service (409). A type without a synchronizer has no resources (a not_sync type): rules on it grant access to the type as a whole.",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Type resource"},
	}, requireWholeType(model.PlatformTypeResource, model.ActionWrite)), func(ctx context.Context, in *struct {
		ServicePath
		Body model.TypeResourceInput
	}) (*server.Out[*model.TypeResource], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateTypeResource(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_type_resources",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/type_resource",
		Summary:     "Resource types of a service",
		Tags:        []string{"Type resource"},
	}, requireAny(model.PlatformTypeResource, model.ActionRead)), func(ctx context.Context, in *ServicePath) (*server.Out[[]model.TypeResource], error) {
		out, err := h.app.ListTypeResources(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformTypeResource, out, func(t model.TypeResource) uuid.UUID { return t.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_type_resource",
		Method:      http.MethodPatch,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}",
		Summary:     "Update a resource type",
		Description: "Replaces the name, the parents and the level hierarchy. Cycles in the type graph are rejected (400), the name is unique within the service (409).",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Type resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourcePath
		Body model.TypeResourceInput
	}) (*server.Out[*model.TypeResource], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateTypeResource(ctx, in.ServiceID, in.TypeResourceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_type_resource",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}",
		Summary:     "Delete a resource type",
		Description: "Soft delete. Not allowed while the type is referenced by synchronizers, rules or child types (409, extra.blocked_by).",
		Tags:        []string{"Type resource"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *TypeResourcePath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteTypeResource(ctx, in.ServiceID, in.TypeResourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_all_type_resources",
		Method:      http.MethodGet,
		Path:        "/type_resource",
		Summary:     "All resource types of all services",
		Description: "For picking a parent across services: a type's parent may belong to another service.",
		Tags:        []string{"Type resource"},
	}, requireAny(model.PlatformTypeResource, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.TypeResourceWithService], error) {
		out, err := h.app.ListAllTypeResources(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformTypeResource, out, func(t model.TypeResourceWithService) uuid.UUID { return t.ID }))
	})
}

// ---------- Resource + Synchronizer ----------

type ResourceQuery struct {
	TypeResourcePath
	ResourceID string `query:"resource_id" required:"true" doc:"Resource id in the external system" example:"b1"`
}

type CoveredQuery struct {
	ResourceQuery
	GrantedResourceID string `query:"granted_resource_id" required:"true" doc:"Id of the resource the grant was issued on" example:"d1"`
}

func (h *adminHandlers) registerResources(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_resources_of_type",
		Method:      http.MethodGet,
		Path:        "/type_resource/{type_resource_id}/resource",
		Summary:     "Resources of a type from its synchronizer",
		Description: "Resources of the type from the local cache, first synchronized with the active synchronizer; sorted by name. Used by the UI to pick a specific resource_id. Empty if the type has no synchronizer; if the source is unavailable, the last synchronized state is returned.",
		Tags:        []string{"Resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionRead, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourceID  uuid.UUID `path:"type_resource_id" format:"uuid" doc:"Resource type id"`
		IncludeInactive bool      `query:"include_inactive" doc:"Also return the resources the source no longer reports (they are kept, switched off: active=false)"`
	}) (*server.Out[[]model.Resource], error) {
		out, err := h.app.ListResources(ctx, in.TypeResourceID, in.IncludeInactive)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "refresh_resources_of_type",
		Method:      http.MethodPost,
		Path:        "/type_resource/{type_resource_id}/resource/refresh",
		Summary:     "Refresh the resources of a type from its source now",
		Description: "Asks the source of the type right away, skipping the 30-second throttle and the background refresh, and brings the local copy up to date. 422 — the type has no source; 424 — the source did not answer (the data stays as it was).",
		Tags:        []string{"Resource"},
		Errors:      []int{http.StatusUnprocessableEntity, http.StatusFailedDependency},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourceID uuid.UUID `path:"type_resource_id" format:"uuid" doc:"Resource type id"`
	}) (*server.Out[*model.ResourceRefresh], error) {
		out, err := h.app.RefreshResources(ctx, in.TypeResourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_resource",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/resource",
		Summary:     "Find a resource by id",
		Description: "Fetches the resource from the source (updating the cache); if the source is unavailable, returns the last cached value. A resource that disappeared from the source is not found.",
		Tags:        []string{"Resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionRead, "type_resource_id")), func(ctx context.Context, in *ResourceQuery) (*server.Out[*model.Resource], error) {
		out, err := h.app.GetResource(ctx, in.TypeResourceID, in.ResourceID)
		if err != nil {
			return nil, err
		}
		if out == nil {
			return nil, apperr.NotFound(i18n.ResourceNotFound, in.ResourceID)
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "check_resource_covered",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/resource/covered",
		Summary:     "Whether a grant on a resource covers its descendant",
		Description: "true if granted_resource_id is resource_id itself or its ancestor along the parent_id chain (up to 32 levels).",
		Tags:        []string{"Resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionRead, "type_resource_id")), func(ctx context.Context, in *CoveredQuery) (*server.Out[bool], error) {
		out, err := h.app.IsCovered(ctx, in.TypeResourceID, in.ResourceID, in.GrantedResourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "who_has_access",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/access",
		Summary:     "Who has access to a resource",
		Description: "The reverse of a check: the rules in force that cover the resource (or, without resource_id, the type as a whole), denials first, each with the levels that reach it, whom it applies to and — from the registry of known subjects — the people it reaches. It lists rules, not decisions: a denial may be lifted by an exception, and a person may be reached by several rules; to know what one subject gets, ask the check. Rules that find their resource from a claim are listed apart. Needs read on all assignment rules, because it shows them whatever their target.",
		Tags:        []string{"Resource"},
	}, requireWholeType(model.PlatformAssignmentRule, model.ActionRead)), func(ctx context.Context, in *struct {
		TypeResourcePath
		ResourceID string `query:"resource_id" required:"false" doc:"The resource; omitted or empty — the type as a whole" example:"b1"`
	}) (*server.Out[*model.AccessLookup], error) {
		var resourceID *string
		if in.ResourceID != "" {
			resourceID = &in.ResourceID
		}
		out, err := h.app.WhoHasAccess(ctx, in.ServiceID, in.TypeResourceID, resourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	const syncPath = "/service/{service_id}/type_resource/{type_resource_id}/resource/synchronizer"
	const syncDescription = "Before saving, the source is checked by fetching the resource probe_resource_id: if it is not found or the source is unavailable — 424. The single-resource SQL query uses the :id parameter and must return the columns id, name, parent_id; an HTTP source must serve GET {url}/{id} and GET {url}, optionally with the configured headers. Every other column (SQL) or field (HTTP) of a resource becomes its attribute (meta: strings or lists of strings) that rules can match a claim against (resource_from_claim.match_by = meta.<name>). Secrets (the password in the DSN, header values) are returned masked as ********; sending the mask back in an update keeps the stored value."

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_synchronizer",
		Method:      http.MethodPost,
		Path:        syncPath,
		Summary:     "Create a synchronizer",
		Description: syncDescription + " A resource type has one source: when it already has one the answer is 409 — change that one.",
		Tags:        []string{"Resource"},
		Errors:      []int{http.StatusConflict, http.StatusFailedDependency},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourcePath
		Body model.SynchronizerInput
	}) (*server.Out[*model.Synchronizer], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateSynchronizer(ctx, in.ServiceID, in.TypeResourceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "test_synchronizer",
		Method:      http.MethodPost,
		Path:        syncPath + "/test",
		Summary:     "Try a source without saving it",
		Description: "Asks the configuration for its whole list and reports what Licetum would make of it: the number of resources, the first few, repeated ids, parents that are not known and the attributes. Nothing is saved. Send synchronizer_id when the configuration edits a saved source: a secret sent back masked is taken from it. 424 — the source did not answer.",
		Tags:        []string{"Resource"},
		Errors:      []int{http.StatusFailedDependency},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourcePath
		Body model.SynchronizerTestInput
	}) (*server.Out[*model.SourceTestResult], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.TestSource(ctx, in.ServiceID, in.TypeResourceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_synchronizers",
		Method:      http.MethodGet,
		Path:        syncPath,
		Summary:     "Synchronizers of a resource type",
		Tags:        []string{"Resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionRead, "type_resource_id")), func(ctx context.Context, in *TypeResourcePath) (*server.Out[[]model.Synchronizer], error) {
		out, err := h.app.ListSynchronizers(ctx, in.ServiceID, in.TypeResourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_synchronizer",
		Method:      http.MethodPatch,
		Path:        syncPath + "/{synchronizer_id}",
		Summary:     "Update a synchronizer",
		Description: syncDescription,
		Tags:        []string{"Resource"},
		Errors:      []int{http.StatusFailedDependency},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		SynchronizerPath
		Body model.SynchronizerInput
	}) (*server.Out[*model.Synchronizer], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateSynchronizer(ctx, in.ServiceID, in.TypeResourceID, in.SynchronizerID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_synchronizer",
		Method:      http.MethodDelete,
		Path:        syncPath + "/{synchronizer_id}",
		Summary:     "Delete a synchronizer",
		Description: "Soft delete. A type left without a synchronizer has no resources any more (it becomes not_sync), and rules on its specific resources stop taking effect (target_valid=false).",
		Tags:        []string{"Resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *SynchronizerPath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteSynchronizer(ctx, in.ServiceID, in.TypeResourceID, in.SynchronizerID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// ---------- Group + GroupMember ----------

func (h *adminHandlers) registerGroups(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_group",
		Method:      http.MethodPost,
		Path:        "/group",
		Summary:     "Create a group",
		Description: "Group names are globally unique (409 on a duplicate).",
		Tags:        []string{"Group"},
		Errors:      []int{http.StatusConflict},
	}, requireWholeType(model.PlatformGroup, model.ActionWrite)), func(ctx context.Context, in *struct{ Body model.GroupInput }) (*server.Out[*model.Group], error) {
		out, err := h.app.CreateGroup(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_groups",
		Method:      http.MethodGet,
		Path:        "/group",
		Summary:     "List groups",
		Tags:        []string{"Group"},
	}, requireAny(model.PlatformGroup, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.Group], error) {
		out, err := h.app.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformGroup, out, func(g model.Group) uuid.UUID { return g.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_group",
		Method:      http.MethodPatch,
		Path:        "/group/{group_id}",
		Summary:     "Rename a group",
		Tags:        []string{"Group"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformGroup, model.ActionWrite, "group_id")), func(ctx context.Context, in *struct {
		GroupPath
		Body model.GroupInput
	}) (*server.Out[*model.Group], error) {
		out, err := h.app.UpdateGroup(ctx, in.GroupID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_group",
		Method:      http.MethodDelete,
		Path:        "/group/{group_id}",
		Summary:     "Delete a group",
		Description: "Not allowed while the group is referenced by rules or memberships (409, extra.blocked_by).",
		Tags:        []string{"Group"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformGroup, model.ActionWrite, "group_id")), func(ctx context.Context, in *GroupPath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteGroup(ctx, in.GroupID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	const memberDescription = "Membership is defined either by a predicate (AND conditions over token claims; an empty list matches any subject) or by member_group_id (all members of the nested group become members of this one). Nesting is resolved transitively."

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_group_member",
		Method:      http.MethodPost,
		Path:        "/group/{group_id}/member",
		Summary:     "Add a membership rule to a group",
		Description: memberDescription,
		Tags:        []string{"Group member"},
		Errors:      []int{http.StatusBadRequest},
	}, requireWholeType(model.PlatformGroupMember, model.ActionWrite)), func(ctx context.Context, in *struct {
		GroupPath
		Body model.GroupMemberInput
	}) (*server.Out[*model.GroupMember], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateGroupMember(ctx, in.GroupID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_group_members",
		Method:      http.MethodGet,
		Path:        "/group/{group_id}/member",
		Summary:     "Membership rules of a group",
		Tags:        []string{"Group member"},
	}, requireAny(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *GroupPath) (*server.Out[[]model.GroupMember], error) {
		out, err := h.app.ListGroupMembers(ctx, in.GroupID)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformGroupMember, out, func(m model.GroupMember) uuid.UUID { return m.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_group_member",
		Method:      http.MethodPatch,
		Path:        "/group/{group_id}/member/{member_id}",
		Summary:     "Replace a membership rule",
		Description: memberDescription,
		Tags:        []string{"Group member"},
	}, requireResource(model.PlatformGroupMember, model.ActionWrite, "member_id")), func(ctx context.Context, in *struct {
		GroupMemberPath
		Body model.GroupMemberInput
	}) (*server.Out[*model.GroupMember], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateGroupMember(ctx, in.GroupID, in.MemberID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_group_member",
		Method:      http.MethodDelete,
		Path:        "/group/{group_id}/member/{member_id}",
		Summary:     "Delete a membership rule",
		Tags:        []string{"Group member"},
	}, requireResource(model.PlatformGroupMember, model.ActionWrite, "member_id")), func(ctx context.Context, in *GroupMemberPath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteGroupMember(ctx, in.GroupID, in.MemberID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// ---------- Role + RolePermission ----------

func (h *adminHandlers) registerRoles(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_role",
		Method:      http.MethodPost,
		Path:        "/role",
		Summary:     "Create a role",
		Description: "A role is a named set of permissions: add them with the role permission endpoints, then grant the role with an assignment rule (role_id). Role names are globally unique (409 on a duplicate).",
		Tags:        []string{"Role"},
		Errors:      []int{http.StatusConflict},
	}, requireWholeType(model.PlatformRole, model.ActionWrite)), func(ctx context.Context, in *struct{ Body model.RoleInput }) (*server.Out[*model.Role], error) {
		out, err := h.app.CreateRole(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_roles",
		Method:      http.MethodGet,
		Path:        "/role",
		Summary:     "List roles",
		Tags:        []string{"Role"},
	}, requireAny(model.PlatformRole, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.Role], error) {
		out, err := h.app.ListRoles(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformRole, out, func(r model.Role) uuid.UUID { return r.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_role",
		Method:      http.MethodPatch,
		Path:        "/role/{role_id}",
		Summary:     "Rename a role",
		Tags:        []string{"Role"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformRole, model.ActionWrite, "role_id")), func(ctx context.Context, in *struct {
		RolePath
		Body model.RoleInput
	}) (*server.Out[*model.Role], error) {
		out, err := h.app.UpdateRole(ctx, in.RoleID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_role",
		Method:      http.MethodDelete,
		Path:        "/role/{role_id}",
		Summary:     "Delete a role",
		Description: "Not allowed while rules grant the role (409, extra.blocked_by). Its permission items are deleted with it.",
		Tags:        []string{"Role"},
		Errors:      []int{http.StatusConflict},
	}, requireResource(model.PlatformRole, model.ActionWrite, "role_id")), func(ctx context.Context, in *RolePath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteRole(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	// The items are part of their role: access to them is access to the role itself.
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_role_permission",
		Method:      http.MethodPost,
		Path:        "/role/{role_id}/permission",
		Summary:     "Add a permission to a role",
		Description: rolePermissionDescription,
		Tags:        []string{"Role permission"},
		Errors:      []int{http.StatusBadRequest},
	}, requireResource(model.PlatformRole, model.ActionWrite, "role_id")), func(ctx context.Context, in *struct {
		RolePath
		Body model.RolePermissionInput
	}) (*server.Out[*model.RolePermission], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		if err := h.checkPlatformTargets(ctx, in.Body.TypeResourceID); err != nil {
			return nil, err
		}
		out, err := h.app.CreateRolePermission(ctx, in.RoleID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_role_permissions",
		Method:      http.MethodGet,
		Path:        "/role/{role_id}/permission",
		Summary:     "Permissions of a role",
		Tags:        []string{"Role permission"},
	}, requireResource(model.PlatformRole, model.ActionRead, "role_id")), func(ctx context.Context, in *RolePath) (*server.Out[[]model.RolePermissionWithContext], error) {
		out, err := h.app.ListRolePermissions(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_role_permission",
		Method:      http.MethodPatch,
		Path:        "/role/{role_id}/permission/{permission_id}",
		Summary:     "Replace a permission of a role",
		Description: rolePermissionDescription,
		Tags:        []string{"Role permission"},
	}, requireResource(model.PlatformRole, model.ActionWrite, "role_id")), func(ctx context.Context, in *struct {
		RolePermissionPath
		Body model.RolePermissionInput
	}) (*server.Out[*model.RolePermission], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		current, err := h.app.RolePermissionTarget(ctx, in.RoleID, in.PermissionID)
		if err != nil {
			return nil, err
		}
		if err := h.checkPlatformTargets(ctx, current, in.Body.TypeResourceID); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateRolePermission(ctx, in.RoleID, in.PermissionID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_role_permission",
		Method:      http.MethodDelete,
		Path:        "/role/{role_id}/permission/{permission_id}",
		Summary:     "Delete a permission of a role",
		Tags:        []string{"Role permission"},
	}, requireResource(model.PlatformRole, model.ActionWrite, "role_id")), func(ctx context.Context, in *RolePermissionPath) (*server.Out[uuid.UUID], error) {
		current, err := h.app.RolePermissionTarget(ctx, in.RoleID, in.PermissionID)
		if err != nil {
			return nil, err
		}
		if err := h.checkPlatformTargets(ctx, current); err != nil {
			return nil, err
		}
		out, err := h.app.DeleteRolePermission(ctx, in.RoleID, in.PermissionID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// checkPlatformTargets: an item grants access on its target, so an item on a platform policy needs the access
// that creating a rule on that policy needs — write on the policy as a whole. A point write on the role does
// not reach the platform, or a role editor could grant himself rights over the admin API through a role he holds.
func (h *adminHandlers) checkPlatformTargets(ctx context.Context, typeIDs ...uuid.UUID) error {
	return authorizePlatformTargets(ctx, h.app, typeIDs...)
}

const rolePermissionDescription = "An item grants access levels on a target of a resource type, the same way an inline rule does: " +
	"the whole type (no resource_id and no resource_from_claim), a resource (resource_id) or a resource computed from a claim " +
	"of the subject (resource_from_claim). A specific resource can only be targeted on a type with a synchronizer (400 otherwise). " +
	"'*' is not allowed in a role: denying everything is possible only with an inline deny rule. " +
	"An item on a platform policy (the self-service types) needs write on that policy as a whole, as creating a rule on it does."

// ---------- AssignmentRule ----------

const ruleDescription = `What it grants — either **role_id** (all permissions of a role; each item of the role keeps its own
target and access levels) or an inline permission: **type_resource_id** with **access** (for example ["read","write"]),
on a specific resource (**resource_id**), on a resource computed from a claim at check time (**resource_from_claim**)
or on the whole type (neither). The grant cascades to all resources of child types, including types of other
services. A specific resource can only be targeted on a type with a synchronizer (400 otherwise): a type without one
has no resources, and a rule on it grants access to the type as a whole (a not_sync type).

Who the rule applies to — exactly one of: **subject** (a specific user or a group written as group:{uuid}),
**predicate** (AND conditions over token claims) or **member_of_group_id** (all members of a group). If none is set,
the rule applies to everyone.

**effect** allow (the default) grants the access levels; deny takes them away from everything the rule covers, unless an
exception below it lifts the denial. A role assignment takes the effect of the rule for all of its items.`

func (h *adminHandlers) registerAssignmentRules(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_assignment_rule",
		Method:      http.MethodPost,
		Path:        "/assignment_rule",
		Summary:     "Create an access rule",
		Description: ruleDescription,
		Tags:        []string{"AssignmentRule"},
	}, requireWholeType(model.PlatformAssignmentRule, model.ActionWrite)), func(ctx context.Context, in *struct {
		Body model.AssignmentRuleInput
	}) (*server.Out[*model.AssignmentRule], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		if err := authorizeRuleCreate(ctx, h.app, &in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateAssignmentRule(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "preview_assignment_rule",
		Method:      http.MethodPost,
		Path:        "/assignment_rule/preview",
		Summary:     "Preview an access rule",
		Description: "Says what the rule would do if it were created, and creates nothing: how many resources it covers through the hierarchy, which known people it reaches, and which denials and exceptions in force meet it (a denial above it cancels what it grants, one inside it carves a part out) and which rules already give the same. The body is that of POST /assignment_rule and is checked the same way.",
		Tags:        []string{"AssignmentRule"},
	}, requireWholeType(model.PlatformAssignmentRule, model.ActionWrite)), func(ctx context.Context, in *struct {
		Body model.AssignmentRuleInput
	}) (*server.Out[*model.GrantPreview], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		if err := authorizeRuleCreate(ctx, h.app, &in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.PreviewGrant(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_assignment_rules",
		Method:      http.MethodGet,
		Path:        "/assignment_rule",
		Summary:     "All access rules",
		Description: "Rules of every service and type, with service and type names, sorted by them. target_valid=false marks a rule on a specific resource of a type that has no synchronizer (e.g. after it was deleted), or a rule that looks a resource up in, or climbs through, such a type: such a rule does not take effect (a denial of that kind still applies below the links to the resources the type left).",
		Tags:        []string{"AssignmentRule"},
	}, requireAny(model.PlatformAssignmentRule, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.AssignmentRuleWithContext], error) {
		out, err := h.app.ListAssignmentRules(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(filterAllowed(ctx, model.PlatformAssignmentRule, out, func(r model.AssignmentRuleWithContext) uuid.UUID { return r.ID }))
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_assignment_rule",
		Method:      http.MethodPatch,
		Path:        "/assignment_rule/{rule_id}",
		Summary:     "Update an access rule",
		Description: "Replaces the whole rule (not a partial update). The target is re-checked only when it changes, so a rule that stopped taking effect can still be disabled or edited.\n\n" + ruleDescription,
		Tags:        []string{"AssignmentRule"},
	}, requireResource(model.PlatformAssignmentRule, model.ActionWrite, "rule_id")), func(ctx context.Context, in *struct {
		RulePath
		Body model.AssignmentRuleInput
	}) (*server.Out[*model.AssignmentRule], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		old, err := h.app.AssignmentRuleOf(ctx, in.RuleID)
		if err != nil {
			return nil, err
		}
		if err := authorizeRuleUpdate(ctx, h.app, old, &in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateAssignmentRule(ctx, in.RuleID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_assignment_rule",
		Method:      http.MethodDelete,
		Path:        "/assignment_rule/{rule_id}",
		Summary:     "Delete an access rule",
		Tags:        []string{"AssignmentRule"},
	}, requireResource(model.PlatformAssignmentRule, model.ActionWrite, "rule_id")), func(ctx context.Context, in *RulePath) (*server.Out[uuid.UUID], error) {
		old, err := h.app.AssignmentRuleOf(ctx, in.RuleID)
		if err != nil {
			return nil, err
		}
		if err := authorizeRuleDelete(ctx, h.app, old); err != nil {
			return nil, err
		}
		out, err := h.app.DeleteAssignmentRule(ctx, in.RuleID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// ---------- Platform ----------

func (h *adminHandlers) registerPlatform(api huma.API) {
	server.Register(api, huma.Operation{
		OperationID: "get_platform",
		Method:      http.MethodGet,
		Path:        "/platform",
		Summary:     "Self-service id",
		Description: "Id of the service under which Licetum is registered within itself; 404 means the self-service is not seeded (licetum migrate was not run).",
		Tags:        []string{"Platform"},
	}, func(ctx context.Context, _ *struct{}) (*server.Out[*model.Platform], error) {
		out, err := h.app.GetPlatform(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_platform_me",
		Method:      http.MethodGet,
		Path:        "/platform/me",
		Summary:     "My access to the platform",
		Description: "What the current user may do with the platform's own resource types — the admin UI uses it to decide what to show and allow. whole_type lists the levels granted on a type as a whole (creating new entities requires write here); entries lists point access to individual entities beyond that.",
		Tags:        []string{"Platform"},
	}, signedIn()), func(ctx context.Context, _ *struct{}) (*server.Out[*model.PlatformAccess], error) {
		return server.OK(accessFrom(ctx).Summary())
	})
}
