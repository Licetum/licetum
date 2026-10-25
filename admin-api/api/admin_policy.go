package api

import (
	"context"
	"net/http"

	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// PolicyImportQuery is the filter of POST /service/{service_id}/policy/import.
type PolicyImportQuery struct {
	DryRun bool `query:"dry_run" required:"false" doc:"Return the plan of the import and apply nothing"`
	Prune  bool `query:"prune" required:"false" doc:"Also delete the rules of the service that the document does not have. Without it, such rules are kept"`
}

// ScenarioPath names a stored scenario of a service.
type ScenarioPath struct {
	ServicePath
	ScenarioID uuid.UUID `path:"scenario_id" format:"uuid" doc:"Scenario id"`
}

// registerPolicy registers the tools of the policy of a service: the tests with their coverage, the linter, the export
// of the policy as a document, and the import of a document. Reading any of them needs read on the service; an import
// writes what its plan says, and each write needs the policy of the admin route that makes it.
func (h *adminHandlers) registerPolicy(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "run_policy_tests",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/policy/test",
		Summary:     "Run policy tests",
		Description: "Answers the scenarios as the simulation does, with the subjects' claims and request attributes, and the data as it is now. Each expectation says whether the answer must be allowed or must not be. The report names the rules that decided each answer, and the coverage: the rules in force that apply to the service's resource types, and those that no scenario reached. Nothing is stored by this call: the scenarios a service keeps are under /policy/scenarios.",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *struct {
		ServicePath
		Body model.PolicyTestInput
	}) (*server.Out[*model.PolicyTestReport], error) {
		out, err := h.app.RunPolicyTests(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_policy_scenarios",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/policy/scenarios",
		Summary:     "Stored scenarios of a service",
		Description: "The scenarios of the policy tests that the service keeps, so that everyone who manages it runs the same ones. They are test fixtures: they change no answer.",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[[]model.PolicyScenario], error) {
		out, err := h.app.ListScenarios(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_policy_scenario",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/policy/scenarios",
		Summary:     "Store a scenario",
		Description: "Keeps a scenario with the service. The name is unique within the service (409).",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *struct {
		ServicePath
		Body model.PolicyTestCase
	}) (*server.Out[*model.PolicyScenario], error) {
		out, err := h.app.CreateScenario(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_policy_scenario",
		Method:      http.MethodPut,
		Path:        "/service/{service_id}/policy/scenarios/{scenario_id}",
		Summary:     "Replace a stored scenario",
		Description: "Replaces the scenario, its name included. The name stays unique within the service (409).",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *struct {
		ScenarioPath
		Body model.PolicyTestCase
	}) (*server.Out[*model.PolicyScenario], error) {
		out, err := h.app.UpdateScenario(ctx, in.ServiceID, in.ScenarioID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_policy_scenario",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}/policy/scenarios/{scenario_id}",
		Summary:     "Delete a stored scenario",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *ScenarioPath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteScenario(ctx, in.ServiceID, in.ScenarioID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "policy_properties",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/policy/properties",
		Summary:     "The properties of the subject and the context attributes the policy of a service reads",
		Description: "The claims and the request attributes the enabled rules that apply to the service's resource types (and to the types above them) read — in their conditions and guards and in claim-based targets, and those of their roles — and the conditions of the group memberships, with how many times each is read. The claims are the keys of subject.properties, the attributes the keys of context, that a PEP of the service has to send for the answers to be the real ones.",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[[]model.PolicyClaim], error) {
		out, err := h.app.PolicyClaims(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "lint_policy",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/policy/lint",
		Summary:     "Lint the policy of a service",
		Description: "Finds the rules that decide nothing (disabled, outside their validity, naming a resource of a type without a synchronizer, guarding on a group without members), the rules that grant more than they seem to (to everyone who signs in, or denying on a claim a token may lack), the duplicates, and the entities nothing uses (roles, access groups). Each finding has a severity, a stable code and a message in the request language.",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[*model.LintReport], error) {
		out, err := h.app.Lint(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "export_policy",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/policy/export",
		Summary:     "Export the policy of a service as a document",
		Description: "The rules of the service, the groups and roles they use (with the groups nested in them), and the access groups of the resource types involved, with names instead of ids (licetum.policy/v1). The document is what POST /service/{service_id}/policy/import takes back.",
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[*model.PolicyDocument], error) {
		out, err := h.app.ExportPolicy(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "import_policy",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/policy/import",
		Summary:     "Import a policy document",
		Description: "Makes the policy of the service match the document: missing groups, roles, access groups and rules are created, the ones that differ are rewritten, and nothing else is touched (with prune, the rules the document does not have are deleted too). Rules are matched by name, or by the id of a rule-<id> name. With dry_run, the plan is returned: the operations and what they do to each entity. Without it, the plan is applied in one transaction, as POST /batch applies it, and the caller needs the policy of every write.",
		Errors:      []int{http.StatusConflict, http.StatusUnprocessableEntity},
		Tags:        []string{"Policy"},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *struct {
		ServicePath
		PolicyImportQuery
		Body model.PolicyDocument
	}) (*server.Out[*model.PolicyImportPlan], error) {
		plan, err := h.app.PlanPolicyImport(ctx, in.ServiceID, &in.Body, in.Prune)
		if err != nil {
			return nil, err
		}
		plan.DryRun = in.DryRun
		if in.DryRun || len(plan.Operations) == 0 {
			plan.Results = []model.BatchResult{}
			return server.OK(plan)
		}
		if err := authorizeOperations(ctx, h.app, plan.Operations); err != nil {
			return nil, err
		}
		plan.Results, err = h.app.RunBatch(ctx, plan.Operations)
		if err != nil {
			return nil, err
		}
		return server.OK(plan)
	})
}
