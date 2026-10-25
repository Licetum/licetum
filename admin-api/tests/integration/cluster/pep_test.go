//go:build integration

package cluster_test

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// revocationSLO is how long a PDP may go on accepting what an administrator revoked: the interval at which a replica reads the
// revisions (REVISION__POLL_INTERVAL, 250 ms) with room for a loaded runner. The test also asserts the delay it measured.
const revocationSLO = 5 * time.Second

// TestContract_PEP02_APIKeysAreHashedRotateAndARevokedKeyIsRefusedByEveryPDP: an API key is stored as the hash of its secret, a
// client holds two active keys at most so a key is rotated, and a key that is revoked, like a client that is disabled, is refused
// by every replica within the revocation SLO; an enabled client and a new key are accepted again.
func TestContract_PEP02_APIKeysAreHashedRotateAndARevokedKeyIsRefusedByEveryPDP(t *testing.T) {
	c := startReplicas(t, 3)
	p := c.SeedPolicy("org")
	base := "/service/" + p.ServiceID + "/pep-clients"
	client := c.Must(http.MethodPost, base, map[string]any{"name": "checkout", "kind": "api_key"})["id"].(string)

	issue := func() (key, keyID string) {
		t.Helper()
		out := c.Must(http.MethodPost, base+"/"+client+"/keys", nil)
		return out["key"].(string), out["key_id"].(string)
	}
	key1, id1 := issue()
	key2, id2 := issue()

	// Stored as the hash of the secret, never the key.
	secret := key1[len("lct_")+16+1:] // the secret is base64url, which has underscores of its own
	var stored []byte
	if err := c.Pool.QueryRow(t.Context(), `SELECT sha256 FROM pep_credential WHERE key_id = $1`, id1).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256([]byte(secret)); string(stored) != string(want[:]) {
		t.Fatalf("the stored hash is not the sha256 of the secret")
	}
	var dump string
	if err := c.Pool.QueryRow(t.Context(), `SELECT string_agg(k::text, ' ') FROM pep_credential k`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, secret) || strings.Contains(dump, key1) {
		t.Fatalf("pep_credential holds the secret of a key: %s", dump)
	}

	// A third active key is refused: the client rotates by revoking one.
	if status, raw := c.AdminDo(http.MethodPost, base+"/"+client+"/keys", nil); status != http.StatusConflict {
		t.Fatalf("a third active key = %d (%s), want 409", status, raw)
	}
	// The list never carries a secret.
	status, raw := c.AdminDo(http.MethodGet, base+"/"+client+"/keys", nil)
	if status != http.StatusOK || strings.Contains(string(raw), secret) || !strings.Contains(string(raw), id1) || !strings.Contains(string(raw), id2) {
		t.Fatalf("the list of keys = %d %s", status, raw)
	}

	ask := authzenreq.Evaluation(p.Slug, "alice", authzenreq.On("branch", "b1"), "read")
	statuses := func(key string) []int {
		out := make([]int, len(c.Replicas))
		for i, r := range c.Replicas {
			out[i] = r.Send(c, ask, key).Status
		}
		return out
	}
	allAre := func(key string, want int) bool {
		for _, s := range statuses(key) {
			if s != want {
				return false
			}
		}
		return true
	}
	waitAll := func(what, key string, want int) time.Duration {
		t.Helper()
		started := time.Now()
		eventually(t, revocationSLO, what, func() bool { return allAre(key, want) })
		return time.Since(started)
	}

	waitAll("both keys are accepted by every replica", key1, http.StatusOK)
	waitAll("both keys are accepted by every replica", key2, http.StatusOK)

	// Revoke the first: every replica refuses it within the SLO, the second goes on.
	c.Must(http.MethodDelete, base+"/"+client+"/keys/"+id1, nil)
	delay := waitAll("every replica refuses the revoked key", key1, http.StatusUnauthorized)
	t.Logf("a revoked key was refused by all %d replicas after %s", len(c.Replicas), delay)
	if !allAre(key2, http.StatusOK) {
		t.Fatalf("the second key stopped working with the first: %v", statuses(key2))
	}

	// With one slot free the client rotates onto a new key.
	key3, _ := issue()
	waitAll("the new key is accepted", key3, http.StatusOK)

	// A disabled client is refused with all its keys, an enabled one accepted again.
	c.Must(http.MethodPatch, base+"/"+client, map[string]any{"enabled": false})
	waitAll("a disabled client's key is refused", key2, http.StatusUnauthorized)
	waitAll("a disabled client's new key is refused", key3, http.StatusUnauthorized)
	c.Must(http.MethodPatch, base+"/"+client, map[string]any{"enabled": true})
	waitAll("an enabled client's key is accepted again", key2, http.StatusOK)

	// A deleted client takes its keys with it.
	c.Must(http.MethodDelete, base+"/"+client, nil)
	waitAll("a deleted client's key is refused", key2, http.StatusUnauthorized)
	waitAll("a deleted client's key is refused", key3, http.StatusUnauthorized)
}
