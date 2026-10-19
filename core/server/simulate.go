package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// The simulation of the AuthZEN API for the administrators (ADR 0001, 7.14, 8.5): the evaluations and the searches of a PEP,
// answered for the subject and the properties the request names, without a token of the subject and without a PEP client. The
// requests and the answers are those of the AuthZEN API (the same models, the same engine), and an evaluation always explains
// itself in context.reason_admin. The answer comes from the data as it is now (the freshest read), and nothing is recorded: the
// simulated decisions are not logged and no call is audited. The admin API registers the routes with its own policy
// (RegisterSimulation's decorate); the call is for administrators, since the properties are taken as they are given.

// SimulationPath names the service of a simulated request.
type SimulationPath struct {
	ServiceID uuid.UUID `path:"service_id" format:"uuid" doc:"Service id"`
}

// SimulationTag is the tag of the operations.
const SimulationTag = "Simulation"

// RegisterSimulation registers the simulation of the AuthZEN API under /service/{service_id}/simulate/access/v1/ on the admin
// API. decorate gives each operation the policy and the security of the admin API.
func RegisterSimulation(api huma.API, app *engine.App, cfg *config.Config, decorate func(huma.Operation) huma.Operation) {
	const base = "/service/{service_id}/simulate/access/v1"
	const note = `

The request is the one of the AuthZEN Authorization API 1.0 of the PDP, and so is the answer. The context holds the attributes of the
request that the rules may read. The answer always comes from the data as it is now. Nothing is recorded: the decision is not logged.`
	const person = ` subject.id is the person simulated and subject.properties are their claims, taken as they are given.`
	const searchNote = `

Paging: page.limit is the size of the page (at most 1000, the default), and the response carries page.next_token while there are
more results; send it back as page.token with the same request.`
	errs := []int{http.StatusBadRequest, http.StatusNotFound}
	op := func(id, path, summary, description string) huma.Operation {
		return decorate(huma.Operation{
			OperationID: id, Method: http.MethodPost, Path: base + path, Summary: summary, Description: description + note,
			Tags: []string{SimulationTag}, Errors: errs, MaxBodyBytes: authzenMaxBody,
		})
	}
	simulated := func(serviceID uuid.UUID) *authzenCaller {
		return &authzenCaller{app: app, cfg: cfg, serviceID: serviceID, simulated: true}
	}

	Register(api, op("simulate_evaluation", "/evaluation", "Simulate an AuthZEN access evaluation",
		"Decides whether the subject may perform the action on the resource, as the PDP would."+person+" The answer always carries its explanation in context.reason_admin."),
		func(ctx context.Context, in *struct {
			SimulationPath
			Body model.AuthzenEvaluationRequest
		}) (*Out[model.AuthzenEvaluationResponse], error) {
			if err := in.Body.Validate(); err != nil {
				return nil, badRequest(err)
			}
			if _, aerr := authzenSettings(in.Body.Context); aerr != nil {
				return nil, aerr
			}
			evals, err := simulated(in.ServiceID).evaluate(ctx, engine.HigherConsistency, []model.AuthzenEvaluationRequest{in.Body},
				model.AuthzenEvaluationsRequest{}, true, model.AuthzenExecuteAll)
			if err != nil {
				return nil, err
			}
			if evals[0].err != nil {
				return nil, evals[0].err
			}
			return OK(evals[0].resp)
		})

	Register(api, op("simulate_evaluations", "/evaluations", "Simulate AuthZEN access evaluations (batch)",
		"Decides up to 100 evaluations in one request, with the defaults of the top level and the evaluations_semantic of the options, as the PDP would."+person+" Each answer carries its explanation in context.reason_admin."),
		func(ctx context.Context, in *struct {
			SimulationPath
			Body model.AuthzenEvaluationsRequest
		}) (*Out[model.AuthzenEvaluationsResponse], error) {
			if err := in.Body.Validate(); err != nil {
				return nil, badRequest(err)
			}
			if _, aerr := authzenSettings(in.Body.Context); aerr != nil {
				return nil, aerr
			}
			out, err := simulated(in.ServiceID).evaluations(ctx, engine.HigherConsistency, in.Body, true)
			if err != nil {
				return nil, err
			}
			return OK(*out)
		})

	Register(api, op("simulate_search_resource", "/search/resource", "Simulate an AuthZEN resource search",
		"Lists the resources of the type resource.type on which the subject may perform the action; resource.id is ignored."+person+searchNote),
		func(ctx context.Context, in *struct {
			SimulationPath
			Body model.AuthzenResourceSearchRequest
		}) (*Out[model.AuthzenResourceSearchResponse], error) {
			if err := in.Body.Validate(); err != nil {
				return nil, badRequest(err)
			}
			out, err := simulated(in.ServiceID).searchResources(ctx, engine.HigherConsistency,
				searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Action: in.Body.Action, Context: in.Body.Context, Page: in.Body.Page})
			if err != nil {
				return nil, err
			}
			return OK(*out)
		})

	Register(api, op("simulate_search_action", "/search/action", "Simulate an AuthZEN action search",
		"Lists the actions (access levels) the subject may perform on the resource; without resource.id the resource is the type as a whole."+person+searchNote),
		func(ctx context.Context, in *struct {
			SimulationPath
			Body model.AuthzenActionSearchRequest
		}) (*Out[model.AuthzenActionSearchResponse], error) {
			if err := in.Body.Validate(); err != nil {
				return nil, badRequest(err)
			}
			out, err := simulated(in.ServiceID).searchActions(ctx, engine.HigherConsistency,
				searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Context: in.Body.Context, Page: in.Body.Page})
			if err != nil {
				return nil, err
			}
			return OK(*out)
		})

	Register(api, op("simulate_search_subject", "/search/subject", "Simulate an AuthZEN subject search",
		"Lists the people of the subject directory of the service, of the type subject.type, who may perform the action on the resource, decided with their stored properties; subject.id is not used."+searchNote),
		func(ctx context.Context, in *struct {
			SimulationPath
			Body model.AuthzenSubjectSearchRequest
		}) (*Out[model.AuthzenSubjectSearchResponse], error) {
			if err := in.Body.Validate(); err != nil {
				return nil, badRequest(err)
			}
			out, err := simulated(in.ServiceID).searchSubjects(ctx, engine.HigherConsistency,
				searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Action: in.Body.Action, Context: in.Body.Context, Page: in.Body.Page})
			if err != nil {
				return nil, err
			}
			return OK(*out)
		})
}
