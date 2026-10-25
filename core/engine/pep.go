package engine

import (
	"context"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/pepauth"
)

// The PEP directory is what the check API authenticates a PEP with: the verifiers of the identity providers, the enabled clients
// of each service and the credentials of the API keys. The process keeps it per revision of the pep scope. The revision comes from
// the view of the process (see cacheState), which the requests and WatchRevision renew like the policy revisions, so a warm request
// touches no database. The directory is loaded again only when that revision moved, once for the requests that wait.

// pepLoadTimeout bounds the load of the directory (one statement).
const pepLoadTimeout = 10 * time.Second

// PEPDirectory is the PEP directory as of one revision of the pep scope. The requests of the process share it: it is read-only
// once loaded.
type PEPDirectory struct {
	revision int64
	// Verifiers verifies the tokens of each issuer, by issuer.
	Verifiers map[string]*pepauth.Verifier
	// ClientsByService are the enabled clients of each service.
	ClientsByService map[uuid.UUID][]db.PEPClient
	// Credentials are the API key credentials that are not revoked, by key id.
	Credentials map[string]db.PEPCredential
	// clients are the enabled clients by id, for the credentials.
	clients map[uuid.UUID]db.PEPClient
	// behind is when the process first saw a newer revision of the pep scope than this directory holds (Unix nanoseconds, 0 while
	// the directory is current). It bounds how long a failing reload is covered by this directory (POLICY__MAX_STALENESS).
	behind atomic.Int64
}

// APIKey returns the credential a key id names and its client, when the key authenticates at now: the key is not expired, and its
// client is enabled and not deleted (the directory holds only those). The expiry is read here against the clock, not in the directory:
// it moves with time and not with a write, so no revision of the directory announces it.
func (d *PEPDirectory) APIKey(keyID string, now time.Time) (db.PEPCredential, db.PEPClient, bool) {
	cred, ok := d.Credentials[keyID]
	if !ok || (cred.ExpiresAt != nil && !now.Before(*cred.ExpiresAt)) {
		return db.PEPCredential{}, db.PEPClient{}, false
	}
	client, ok := d.clients[cred.ClientID]
	return cred, client, ok
}

// pepState is what the process keeps of the PEP directory: the last one it loaded, and the loads in progress.
type pepState struct {
	dir   atomic.Pointer[PEPDirectory]
	loads singleflight.Group
}

// keep stores d unless the process holds a directory of a revision at least as new, and returns the one the process holds. Two loads
// that finish out of order never move the directory back.
func (p *pepState) keep(d *PEPDirectory) *PEPDirectory {
	for {
		cur := p.dir.Load()
		if cur != nil && cur.revision >= d.revision {
			return cur
		}
		if p.dir.CompareAndSwap(cur, d) {
			return d
		}
	}
}

// pepDefaultStaleLimit is the hard bound a PDP keeps an answer for, when POLICY__MAX_STALENESS is 0 (no limit): the value the ADR
// sets for it (docs/adr/0001-regional-pdp.md, section 6.3). A revoked key must not authenticate longer than that, so the directory
// has the bound even when the policy has none.
const pepDefaultStaleLimit = 60 * time.Second

// pepStaleLimit is how long the process serves the last directory after a reload fails under a confirmed view.
func pepStaleLimit(t tuning) time.Duration {
	if t.policyMaxStaleness > 0 {
		return t.policyMaxStaleness
	}
	return pepDefaultStaleLimit
}

// PEPDirectory returns the PEP directory of the process, as of the revision of the pep scope the process has read. It is loaded
// again when that revision is newer than the one held. The directory follows the policy: when the view cannot confirm the revisions
// (see cacheState), the directory is read from PostgreSQL and the read fails when PostgreSQL does. When a reload fails while the
// view is confirmed, the last directory is served for at most pepStaleLimit after the process first saw the newer revision, so a
// revoked key stays revoked for no longer than that.
func (a *App) PEPDirectory(ctx context.Context) (*PEPDirectory, error) {
	state := a.cacheState(ctx)
	if !state.ok {
		d, err := a.loadPEPDirectory(ctx)
		if err != nil {
			return nil, err
		}
		return a.pep.keep(d), nil
	}
	cur := a.pep.dir.Load()
	if cur != nil && cur.revision >= state.pep {
		cur.behind.Store(0)
		return cur, nil
	}
	if cur != nil {
		cur.behind.CompareAndSwap(0, time.Now().UnixNano())
	}
	got, err, _ := a.pep.loads.Do(strconv.FormatInt(state.pep, 10), func() (any, error) {
		d, err := a.loadPEPDirectory(ctx)
		if err != nil {
			return nil, err
		}
		return a.pep.keep(d), nil
	})
	if err == nil {
		return got.(*PEPDirectory), nil
	}
	// The failure is logged by loadPEPDirectory. A process that has no directory has nothing to serve, so it fails. A process that
	// has one serves it for at most pepStaleLimit after it first saw the newer revision.
	cur = a.pep.dir.Load()
	if cur == nil {
		return nil, err
	}
	if cur.revision >= state.pep {
		return cur, nil
	}
	cur.behind.CompareAndSwap(0, time.Now().UnixNano())
	if time.Since(time.Unix(0, cur.behind.Load())) >= pepStaleLimit(a.tuningOrDefault()) {
		return nil, err
	}
	return cur, nil
}

// loadPEPDirectory reads the directory in one statement.
func (a *App) loadPEPDirectory(ctx context.Context) (*PEPDirectory, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pepLoadTimeout)
	defer cancel()
	data, err := db.LoadPEPDirectory(ctx, a.DB)
	if err != nil {
		logDependency(ctx, slog.LevelWarn, "failed to read the PEP directory", err)
		return nil, err
	}
	return a.newPEPDirectory(data), nil
}

// newPEPDirectory builds the directory from its rows: a verifier per issuer, the clients by service and the credentials by key id.
func (a *App) newPEPDirectory(data db.PEPDirectoryData) *PEPDirectory {
	d := &PEPDirectory{
		revision:         data.Revision,
		Verifiers:        make(map[string]*pepauth.Verifier, len(data.Issuers)),
		ClientsByService: make(map[uuid.UUID][]db.PEPClient),
		Credentials:      make(map[string]db.PEPCredential, len(data.Credentials)),
		clients:          make(map[uuid.UUID]db.PEPClient, len(data.Clients)),
	}
	for _, i := range data.Issuers {
		d.Verifiers[i.Issuer] = pepauth.NewVerifier(pepauth.IssuerConfig{
			Issuer: i.Issuer, JWKSURL: i.JWKSURL, Audiences: i.Audiences, Algs: i.Algs, RequireTyp: i.RequireTyp,
		}, a.keySets(), a.pdpAudience())
	}
	for _, c := range data.Clients {
		d.ClientsByService[c.ServiceID] = append(d.ClientsByService[c.ServiceID], c)
		d.clients[c.ID] = c
	}
	for _, k := range data.Credentials {
		d.Credentials[k.KeyID] = k
	}
	return d
}

// pdpAudience is the audience a token of a PEP must carry when its issuer lists none: PDP__AUDIENCE, else the identifier of the PDP
// (PDP__PUBLIC_URL). With neither, a token of an issuer that lists no audience is refused: aud is always required.
func (a *App) pdpAudience() string {
	if a.Cfg == nil {
		return ""
	}
	if a.Cfg.PDP.Audience != "" {
		return a.Cfg.PDP.Audience
	}
	return a.Cfg.PDP.PublicURL
}

// AfterPEPWrite is told of a change of the PEP directory this process made (an issuer, a client or a key). The next request reads the
// revisions again and loads the directory. The answers and the policy snapshot stay: no decision reads the directory, so no
// answer key moves.
func (a *App) AfterPEPWrite() { a.invalidateView() }
