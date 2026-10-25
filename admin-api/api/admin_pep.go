package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/server"
)

// PEPClientPath names a PEP client of a service.
type PEPClientPath struct {
	ServicePath
	ClientID uuid.UUID `path:"client_id" format:"uuid" doc:"PEP client id"`
}

// PEPKeyPath names an API key of a PEP client.
type PEPKeyPath struct {
	PEPClientPath
	KeyID string `path:"key_id" minLength:"1" maxLength:"64" doc:"Key id, the part of the key after lct_" example:"3f9a1c0d5b7e2a64"`
}

// pepKeyOut is the answer that issues a key. The key is shown once, so no cache may keep it.
type pepKeyOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         model.PEPKeyIssued
}

// registerPEP: the routes that manage who may call the AuthZEN API (ADR 0001, 7.10). A client belongs to one service, so a
// client is governed by the policy of that service: reading needs read on it, anything else write. The issuers are shared by
// every service, so they need the whole policy. P4 re-gates these routes by the roles of the service.
func (h *adminHandlers) registerPEP(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_pep_clients",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/pep-clients",
		Summary:     "PEP clients of a service",
		Description: "The clients of the service that call its AuthZEN API, by name.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *ServicePath) (*server.Out[[]model.PEPClient], error) {
		out, err := h.app.ListPEPClients(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_pep_client",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/pep-clients",
		Summary:     "Register a PEP client",
		Description: "Registers a client of the service's AuthZEN API. A jwt client is a client application of a registered issuer: its access tokens are accepted when the issuer, the audience and the signature are right. An api_key client holds API keys that Licetum issues (POST …/keys). Operations default to evaluate; explain and the three searches have to be listed. The same client of an issuer in two services is two rows. The name, and the issuer with the client id, are unique within the service (422).",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *struct {
		ServicePath
		Body model.PEPClientInput
	}) (*server.Out[*model.PEPClient], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreatePEPClient(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_pep_client",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/pep-clients/{client_id}",
		Summary:     "A PEP client",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *PEPClientPath) (*server.Out[*model.PEPClient], error) {
		out, err := h.app.GetPEPClient(ctx, in.ServiceID, in.ClientID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_pep_client",
		Method:      http.MethodPatch,
		Path:        "/service/{service_id}/pep-clients/{client_id}",
		Summary:     "Change a PEP client",
		Description: "Changes the fields that are present. The kind does not change. Disabling a client takes effect on every PDP within the revocation delay, without deleting its keys.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *struct {
		PEPClientPath
		Body model.PEPClientPatch
	}) (*server.Out[*model.PEPClient], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdatePEPClient(ctx, in.ServiceID, in.ClientID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_pep_client",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}/pep-clients/{client_id}",
		Summary:     "Delete a PEP client",
		Description: "Deletes the client and revokes its keys. Its name and identity are free again.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *PEPClientPath) (*server.Out[uuid.UUID], error) {
		if err := h.app.DeletePEPClient(ctx, in.ServiceID, in.ClientID); err != nil {
			return nil, err
		}
		return server.OK(in.ClientID)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_pep_keys",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/pep-clients/{client_id}/keys",
		Summary:     "API keys of a PEP client",
		Description: "The keys of the client, revoked ones too, the newest first. A key's secret is never returned, only its id and dates.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound},
	}, requireResource(model.PlatformService, model.ActionRead, "service_id")), func(ctx context.Context, in *PEPClientPath) (*server.Out[[]model.PEPKey], error) {
		out, err := h.app.ListPEPKeys(ctx, in.ServiceID, in.ClientID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "issue_pep_key",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/pep-clients/{client_id}/keys",
		Summary:     "Issue an API key",
		Description: "Issues a key to an api_key client. The key is in this answer and nowhere else: Licetum keeps only its hash, so a lost key is revoked and issued again. A client has at most two active keys (409), so a key is rotated by issuing the new one, moving the PEP to it and revoking the old one.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *PEPClientPath) (*pepKeyOut, error) {
		out, err := h.app.IssuePEPKey(ctx, in.ServiceID, in.ClientID)
		if err != nil {
			return nil, err
		}
		return &pepKeyOut{CacheControl: "no-store", Body: *out}, nil
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "revoke_pep_key",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}/pep-clients/{client_id}/keys/{key_id}",
		Summary:     "Revoke an API key",
		Description: "Revokes the key. Every PDP refuses it within the revocation delay (the revision poll interval). A key that is revoked already stays so.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound},
	}, requireResource(model.PlatformService, model.ActionWrite, "service_id")), func(ctx context.Context, in *PEPKeyPath) (*server.Out[string], error) {
		if err := h.app.RevokePEPKey(ctx, in.ServiceID, in.ClientID, in.KeyID); err != nil {
			return nil, err
		}
		return server.OK(in.KeyID)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_pep_issuers",
		Method:      http.MethodGet,
		Path:        "/platform/pep-issuers",
		Summary:     "PEP issuers",
		Description: "The identity providers whose tokens a PEP may present, by name. They are shared by all services; a client of a service picks one.",
		Tags:        []string{"PEP"},
	}, requireWholeType(model.PlatformService, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[[]model.PEPIssuer], error) {
		out, err := h.app.ListPEPIssuers(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "put_pep_issuer",
		Method:      http.MethodPut,
		Path:        "/platform/pep-issuers",
		Summary:     "Register or replace a PEP issuer",
		Description: "Creates the issuer, or replaces the settings of the one with this name. The iss of a token is compared exactly with the name. A token is accepted only with an aud that contains one of the audiences (or, with none listed, the audience of the PDP), a signature of an allowed algorithm (asymmetric only) and a valid exp and nbf (60 s of leeway). Needs write on the services as a whole.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusUnprocessableEntity},
	}, requireWholeType(model.PlatformService, model.ActionWrite)), func(ctx context.Context, in *struct{ Body model.PEPIssuer }) (*server.Out[*model.PEPIssuer], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.PutPEPIssuer(ctx, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_pep_issuer",
		Method:      http.MethodDelete,
		Path:        "/platform/pep-issuers",
		Summary:     "Delete a PEP issuer",
		Description: "Deletes an issuer that no client uses (409 while one does, a deleted client included). The name is a query parameter because it is a URL.",
		Tags:        []string{"PEP"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, requireWholeType(model.PlatformService, model.ActionWrite)), func(ctx context.Context, in *struct {
		Issuer string `query:"issuer" minLength:"1" maxLength:"2048" doc:"The issuer to delete (its iss)"`
	}) (*server.Out[string], error) {
		if err := h.app.DeletePEPIssuer(ctx, in.Issuer); err != nil {
			return nil, err
		}
		return server.OK(in.Issuer)
	})
}
