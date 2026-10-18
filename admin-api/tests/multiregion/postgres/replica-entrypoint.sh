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
exec docker-entrypoint.sh postgres -c hot_standby=on -c max_connections=400 -c fsync=off
