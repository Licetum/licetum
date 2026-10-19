package httpx

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

const (
	PolicyMetadataKey = "licetum:policy"
	BearerScheme      = "bearer"
)

// BearerToken returns the credential of an Authorization header that uses the Bearer scheme. The scheme name is matched
// without regard to case (RFC 7235, section 2.1), and the credential follows one space after it. Any other scheme, or a
// header with no space in it, is not a bearer credential.
func BearerToken(authorization string) (string, bool) {
	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, BearerScheme) {
		return "", false
	}
	return token, true
}

// AddBearerScheme registers the "bearer" security scheme in the OpenAPI document.
func AddBearerScheme(api huma.API, description string) {
	oapi := api.OpenAPI()
	if oapi.Components.SecuritySchemes == nil {
		oapi.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	oapi.Components.SecuritySchemes[BearerScheme] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description:  description,
	}
}
