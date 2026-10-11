package server

import (
	"github.com/danielgtaylor/huma/v2"
)

const (
	PolicyMetadataKey = "licetum:policy"
	BearerScheme      = "bearer"
)

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
