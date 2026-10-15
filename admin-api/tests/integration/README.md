# Integration, multi-replica and fault injection tests

Tests that need PostgreSQL and Redis. They start disposable containers (Testcontainers; Docker is needed), or use running
servers named by `LICETUM_TEST_POSTGRES_URI` and `LICETUM_TEST_REDIS_URI`.

| directory | build tag | tier | what |
|---|---|---|---|
| `api/` | `integration` | I | the admin and check APIs against real servers |
| `cluster/` | `integration` | M | several check replicas on one PostgreSQL and one Redis; request cost counters; the fakes |
| `chaos/` | `chaos` | C | Toxiproxy in front of PostgreSQL and Redis: latency, cut, black hole, a read-only Redis |
| `harness/` | none | | the helpers the packages above share (not tests) |

```sh
task go:test:integration   # tags integration: api/, cluster/ (and core/tests/integration)
task go:test:chaos         # tag chaos: chaos/ (slower, nightly in .github/workflows/chaos.yml)
```

## The harness

`harness.StartReplicas(t, infra, n, opts...)` builds a `Cluster`: `n` check API replicas, each with **its own** `engine.App`,
database pool, Redis client and HTTP server, on one shared PostgreSQL schema and one shared Redis (which is flushed), next to an
admin API that sets the policy up, a fake identity provider (`support.IdP`) and a fake source of resources (`support.Source`).
Tests of a package run one after another: they share the Redis.

```go
c := harness.StartReplicas(t, infra, 3)
p := c.SeedPolicy("org", 0)                      // a service, two types fed by c.Source over HTTP, a rule for "alice"
res := c.Replicas[1].Permission(c, p.Slug, c.UserToken("alice"))
res.Trace.DBQueries()                            // what the request cost: statements to PostgreSQL ...
res.Trace.RedisRoundTrips()                      // ... and round trips to Redis (a pipeline is one)
c.Replicas[1].Warm(c, "GET", "/service/org/permission", token) // until the cost of a request is steady
```

- **Counting.** The same counts are in `GET /metrics` (`licetum_db_queries_total`, `licetum_db_query_seconds`,
  `licetum_redis_ops_total`, `licetum_redis_op_seconds`). The Trace does not count what the Redis client sends when it opens a
  connection (`HELLO`, ...), nor background work (writing a cache entry); the metrics do. Assert exact counts only after
  `Warm`, or `waitForKeys` for the cache entries to exist.
- **Fakes.** `support.Source` counts the requests per type (`Calls`, `CallsTo`, `History`), can be delayed (`SetDelay`), made to
  fail (`SetStatus`), padded (`SetBodySize`) or held (`Hold`/`Release`, `WaitForCalls`, `InFlight`). `support.IdP` counts the
  requests for its JWKS (`JWKSRequests`), signs tokens (`Token`), rotates its key (`RotateKey`) and can fail (`SetJWKSStatus`).
- **Faults.** `harness.WithFaultProxies()` puts a Toxiproxy proxy in front of PostgreSQL (`c.PostgresFault`) and Redis
  (`c.RedisFault`) for the replicas: `Latency`, `Cut`, `BlackHole`, `Reset`. Toxiproxy runs inside the test process (the
  toxiproxy server library, driven through its HTTP API), so no extra image is needed. `harness.WithReplicaRedis(uri)` points
  the replicas at another Redis, for the read-only test (`REPLICAOF` to an address nobody serves).

Name a test after the acceptance criterion it proves (`TestF3_4_…`) and say in the pull request which criteria it covers.

## The fast read path (F1)

| test | what it proves |
|---|---|
| `cluster/f1_test.go` `TestF1_1`, `_2`, `_3`, `_5`, `_6`, `_7`, `_8` | no query and no Redis command for a warm request; one round trip for an answer only Redis holds; a miss costs the same few queries for 1 000 and for 100 000 rules; slugs and deleted services follow the revision within a second; one key set fetch per TTL, none for bad tokens, one refetch per rotation |
| `api/snapshot_differential_test.go` `TestF1_11` | random policies (validity windows, claim-based and attribute-based rules, roles, nested groups, conditions, denials, exceptions) give the same answers and explanations from the snapshot and from the queries, also after changes and after windows have passed. `LICETUM_DIFF_SEED` repeats a run |
| `chaos/f1_test.go` `TestF1_4`, `_9`, `_10` | a warm request ignores 150 ms on PostgreSQL and Redis; a Redis that answers after 5 s does not pile up goroutines or memory under 600 rps; a skipped Redis adds at most its timeout |

The replicas follow the revision with a delay of up to `REVISION__POLL_INTERVAL`: `Replica.Permission` asks for at least the revision
of the last write made through `AdminDo`/`Must`; use `Replica.Do` for a plain request. Run the packages one at a time (`-p 1`) when they
share one Redis (`LICETUM_TEST_REDIS_URI`).
