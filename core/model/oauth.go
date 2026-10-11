package model

// RefreshTokenInput is the body of POST /service/{id}/oauth/refresh.
type RefreshTokenInput struct {
	_            struct{} `additionalProperties:"true"`
	RefreshToken string   `json:"refresh_token" minLength:"1" doc:"Refresh token issued at sign-in"`
}

// TokenResponse is the token endpoint answer (RFC 6749 §5.1), reduced to what the client needs.
type TokenResponse struct {
	AccessToken      string  `json:"access_token" doc:"Access token"`
	TokenType        string  `json:"token_type" doc:"Token type" example:"Bearer"`
	ExpiresIn        *int64  `json:"expires_in" required:"false" doc:"Access token lifetime, seconds" example:"300"`
	RefreshToken     *string `json:"refresh_token" required:"false" doc:"New refresh token; if the IdP issued one, the old one must be discarded (RFC 6749 §6)"`
	RefreshExpiresIn *int64  `json:"refresh_expires_in" required:"false" doc:"Refresh token lifetime, seconds" example:"1800"`
}
