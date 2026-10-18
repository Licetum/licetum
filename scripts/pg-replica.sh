#!/usr/bin/env bash
# A PostgreSQL primary with one asynchronous streaming replica on this machine, for the tests that need a real replica
# (LICETUM_TEST_POSTGRES_URI is the primary, LICETUM_TEST_REPLICA_ADDR the replica's host:port). Uses the server binaries
# of the machine (no Docker). Must not run as root.
#
#   scripts/pg-replica.sh up [dir]     start both (default dir: ${TMPDIR:-/tmp}/licetum-pg), print the exports
#   scripts/pg-replica.sh down [dir]   stop both and remove the directory
set -euo pipefail

cmd=${1:-up}
dir=${2:-${TMPDIR:-/tmp}/licetum-pg}
primary_port=${PRIMARY_PORT:-5433}
replica_port=${REPLICA_PORT:-5434}

bin=${PG_BIN:-}
if [[ -z $bin ]]; then
  bin=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1 || true)
fi
[[ -x ${bin:-}/initdb ]] || { echo "no PostgreSQL server binaries found (set PG_BIN)" >&2; exit 1; }
[[ $(id -u) -ne 0 ]] || { echo "run as a normal user" >&2; exit 1; }

case $cmd in
  up)
    rm -rf "$dir"
    mkdir -p "$dir"
    "$bin/initdb" -D "$dir/primary" -A trust -U postgres >/dev/null
    "$bin/pg_ctl" -D "$dir/primary" -w -l "$dir/primary.log" \
      -o "-p $primary_port -k $dir -c fsync=off -c max_connections=300 -c wal_level=replica -c max_wal_senders=10 -c hot_standby=on -c wal_keep_size=256MB" start >/dev/null
    "$bin/pg_basebackup" -h 127.0.0.1 -p "$primary_port" -U postgres -D "$dir/replica" -R -X stream >/dev/null
    "$bin/pg_ctl" -D "$dir/replica" -w -l "$dir/replica.log" \
      -o "-p $replica_port -k $dir -c max_connections=300 -c hot_standby=on" start >/dev/null
    echo "export LICETUM_TEST_POSTGRES_URI='postgres://postgres@127.0.0.1:$primary_port/postgres?sslmode=disable'"
    echo "export LICETUM_TEST_REPLICA_ADDR='127.0.0.1:$replica_port'"
    ;;
  down)
    "$bin/pg_ctl" -D "$dir/replica" -m immediate stop >/dev/null 2>&1 || true
    "$bin/pg_ctl" -D "$dir/primary" -m immediate stop >/dev/null 2>&1 || true
    rm -rf "$dir"
    ;;
  *)
    echo "usage: $0 up|down [dir]" >&2
    exit 2
    ;;
esac
