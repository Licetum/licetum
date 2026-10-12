#!/usr/bin/env bash
# The baseline campaign on the 10k profile: five runs per mode, each on a fresh stack (new PostgreSQL and Redis containers,
# a new check server, the policy seeded again), then one ramp to find the knee, then the aggregate report.
#
# Usage: LT=path/to/licetum-loadtest K6=path/to/k6 OUT=reports/baseline scripts/baseline.sh
# Environment: the check binary is ../core/bin/licetum (task loadtest:build builds it). The generator is pinned to CPUs
# 10-11 and the server to CPUs 0-7 (see environment.json for what was pinned). A run that is not valid is recorded and the
# campaign goes on: the validity is the result, not an error to hide.
set -uo pipefail

LT=${LT:?set LT to the licetum-loadtest binary}
K6=${K6:-k6}
OUT=${OUT:-reports/baseline}
PROFILE=${PROFILE:-smoke}
MEASURE=${MEASURE:-600s}
K6_CPUS=${K6_CPUS:-10-11}
CHECK_CPUS=${CHECK_CPUS:-0-7}
CHECK_BIN=${CHECK_BIN:-../core/bin/licetum}

# reset_stack restarts the PostgreSQL and Redis containers of the local test stack (lt-pg, lt-redis), so that every run
# starts with a cold buffer cache and an empty Redis. The harness then creates a database of its own for the run.
reset_stack() {
  docker restart lt-pg lt-redis >/dev/null || { echo "campaign: cannot restart the test stack"; exit 1; }
  until docker exec lt-pg pg_isready -h 127.0.0.1 -p 5432 -U licetum >/dev/null 2>&1; do sleep 1; done
}

run() {
  reset_stack
  "$LT" run -profile "$PROFILE" -k6-bin "$K6" -check-bin "$CHECK_BIN" -k6-cpus "$K6_CPUS" -check-cpus "$CHECK_CPUS" \
    -warm 60s -measure "$MEASURE" "$@"
  echo "campaign: finished $* (exit $?)"
}

# Fixed-rate runs. The rate is chosen from the checks of one minute each: the warm and mixed modes at 200 requests/s,
# the cold mode (every request computed) at 100.
for seed in 1 2 3 4 5; do
  run -mode warm -seed "$seed" -rate 200 -out "$OUT/$PROFILE-warm/run-$seed"
done
for seed in 1 2 3 4 5; do
  run -mode mix -seed "$seed" -rate 200 -out "$OUT/$PROFILE-mix/run-$seed"
done
for seed in 1 2 3 4 5; do
  run -mode cold -seed "$seed" -rate 100 -out "$OUT/$PROFILE-cold/run-$seed"
done

# The ramp: the rate doubles from 50 requests/s, each step two minutes, until a step misses its objectives.
reset_stack
"$LT" run -profile "$PROFILE" -k6-bin "$K6" -check-bin "$CHECK_BIN" -k6-cpus "$K6_CPUS" -check-cpus "$CHECK_CPUS" \
  -mode mix -seed 1 -ramp -rate-start 50 -rate-factor 2 -max-steps 5 -warm 60s -measure 120s -slo-p99 100ms \
  -out "$OUT/$PROFILE-ramp" || echo "campaign: the ramp ended at its objectives"

# The aggregate of the fixed-rate runs, one table per configuration.
"$LT" aggregate -out "$OUT/$PROFILE-report.md" "$OUT/$PROFILE-warm"/run-* "$OUT/$PROFILE-mix"/run-* "$OUT/$PROFILE-cold"/run-*
echo "campaign: done"
