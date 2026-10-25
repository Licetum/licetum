#!/bin/sh
# A streaming replica: takes a base backup of the primary (through the proxy that adds the latency of the link), then starts as a
# standby. The data directory stays between restarts, so a restarted replica only catches up.
set -e
export PGDATA=${PGDATA:-/var/lib/postgresql/data}
mkdir -p "$PGDATA"
chown postgres:postgres "$PGDATA"
chmod 700 "$PGDATA"
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  until gosu postgres pg_basebackup -d "host=$PRIMARY_HOST port=$PRIMARY_PORT user=replicator password=replicator" \
      -D "$PGDATA" -R -X stream; do
    echo "waiting for the primary at $PRIMARY_HOST:$PRIMARY_PORT"
    sleep 2
  done
fi
# A hot standby needs max_connections, max_wal_senders, max_worker_processes, max_prepared_transactions and
# max_locks_per_transaction at least as high as the primary has them (compose.yml, pg-primary): with a lower one the
# recovery aborts at start ("insufficient parameter settings") and the replica never comes up.
# After the link to the primary was cut, the standby asks for the stream again every wal_retrieve_retry_interval (5 s by
# default): with that default a revocation made during the cut reaches the region up to 5 s after the link returned, before the
# replay, which leaves nothing of the SLO of 5 s that F6-5 checks.
exec docker-entrypoint.sh postgres -c hot_standby=on -c max_connections=600 -c max_wal_senders=20 -c fsync=off \
  -c wal_retrieve_retry_interval=1s
