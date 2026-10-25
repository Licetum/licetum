package apperr_test

import (
	"testing"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// WithRealm names the realm in the challenge of a 401 (RFC 6750 §3), before the error parameter when there is one, on a copy:
// the error it was called on is shared by concurrent requests and keeps its own challenge.
func TestWithRealmNamesTheRealmOfTheChallenge(t *testing.T) {
	plain := apperr.Unauthorized(i18n.AuthHeaderRequired)
	named := plain.WithRealm("https://pdp.example.com/service/orders")
	if got := named.GetHeaders().Get("WWW-Authenticate"); got != `Bearer realm="https://pdp.example.com/service/orders"` {
		t.Errorf("challenge = %q", got)
	}
	if got := plain.GetHeaders().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("the original error changed: %q", got)
	}

	invalid := apperr.InvalidToken(i18n.TokenInvalid).WithRealm("r")
	if got := invalid.GetHeaders().Get("WWW-Authenticate"); got != `Bearer realm="r", error="invalid_token"` {
		t.Errorf("challenge = %q", got)
	}

	if got := apperr.Unauthorized(i18n.AuthHeaderRequired).WithRealm(`a"b\c`).GetHeaders().Get("WWW-Authenticate"); got != `Bearer realm="a\"b\\c"` {
		t.Errorf("a quote and a backslash of the realm must be escaped: %q", got)
	}

	// An error without a challenge is returned as it is.
	forbidden := apperr.Forbidden(i18n.AuthzenForbidden)
	if forbidden.WithRealm("r") != forbidden {
		t.Error("a 403 has no challenge to name a realm in")
	}
}
