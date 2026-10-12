package loadtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adminsvc "github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
	support "github.com/licetum/licetum/core/testsupport"
)

const (
	// loadtestIssuer is the iss of every token; the service is registered with it.
	loadtestIssuer = "https://idp.loadtest.invalid"
	// tokenTTL covers a whole run: the tokens are signed once, before the first request.
	tokenTTL = 4 * time.Hour
)

// env is the process side of a run: the database and the cache the harness seeds through the admin application, and
// the identity provider that signs the tokens the check API verifies.
type env struct {
	pool        *pgxpool.Pool
	app         *engine.App
	admin       *adminsvc.App
	dbURI       string
	redisURI    string
	stateSecret string
	idp         *identityProvider
	closers     []func() error
}

// newEnv connects to the infrastructure the same way the binaries do (config from the environment, migrations, the
// engine), so the seeding goes through the same code the admin API runs.
func newEnv(ctx context.Context, infra *support.Infrastructure) (*env, error) {
	secret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	e := &env{dbURI: infra.DBURI, redisURI: infra.RedisURI, stateSecret: secret}
	for name, value := range map[string]string{"DB__URI": e.dbURI, "REDIS__URI": e.redisURI, "AUTH__STATE_SECRET": secret} {
		if err := os.Setenv(name, value); err != nil {
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	e.pool = pool
	e.closers = append(e.closers, func() error { pool.Close(); return nil })
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		e.close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	rdb, err := redisx.Connect(ctx, cfg.Redis.URI)
	if err != nil {
		e.close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	e.closers = append(e.closers, rdb.Close)
	e.app = engine.New(pool, rdb, cfg)
	e.closers = append(e.closers, func() error { e.app.Close(); return nil })
	e.admin = adminsvc.New(e.app)

	e.idp, err = newIdentityProvider()
	if err != nil {
		e.close()
		return nil, err
	}
	e.closers = append(e.closers, e.idp.close)
	return e, nil
}

// close releases the connections, last opened first.
func (e *env) close() {
	for i := len(e.closers) - 1; i >= 0; i-- {
		_ = e.closers[i]()
	}
}

// identityProvider stands in for the IdP of the service: it serves the public key as a JWKS on the loopback
// interface and signs the tokens of the subjects with the matching private key.
type identityProvider struct {
	key     *rsa.PrivateKey
	kid     string
	jwksURL string
	server  *http.Server
}

func newIdentityProvider() (*identityProvider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate the signing key: %w", err)
	}
	kid, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	jwks, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"},
	}})
	if err != nil {
		return nil, fmt.Errorf("encode the JWKS: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the JWKS: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &identityProvider{key: key, kid: kid, jwksURL: "http://" + ln.Addr().String() + "/jwks", server: srv}, nil
}

// token signs an access token for the subject: its preferred_username is the subject, and its department claim picks
// the groups it belongs to.
func (p *identityProvider) token(subject, department string) (string, error) {
	return p.sign(subject, jwt.MapClaims{
		"preferred_username": subject,
		"department":         department,
	})
}

// clientToken signs the token of a client application that asks on behalf of people: azp names the client.
func (p *identityProvider) clientToken(client string) (string, error) {
	return p.sign(client, jwt.MapClaims{"azp": client})
}

func (p *identityProvider) sign(subject string, extra jwt.MapClaims) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": loadtestIssuer,
		"sub": subject,
		"iat": now.Unix(),
		"exp": now.Add(tokenTTL).Unix(),
	}
	maps.Copy(claims, extra)
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = p.kid
	signed, err := t.SignedString(p.key)
	if err != nil {
		return "", fmt.Errorf("sign the token of %s: %w", subject, err)
	}
	return signed, nil
}

// tokens signs one token per subject, spread over the CPUs: RSA signing is the slow part of seeding a large profile.
func (p *identityProvider) tokens(users, groups int) ([]string, error) {
	out := make([]string, users)
	workers := min(users, max(1, runtime.NumCPU()))
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := w; i < users; i += workers {
				tok, err := p.token(subjectName(i), departmentName(i%groups))
				if err != nil {
					errs[w] = err
					return
				}
				out[i] = tok
			}
		})
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

func (p *identityProvider) close() error {
	return p.server.Close()
}

func subjectName(i int) string { return fmt.Sprintf("user-%d", i) }

func departmentName(g int) string { return fmt.Sprintf("dept-%d", g) }

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
