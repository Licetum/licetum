#!/usr/bin/env bash
# The installation test into a kind cluster (F6-12): the chart installs and runs the smoke suite, a rolling upgrade under about 100
# requests a second loses no request, and `helm upgrade` runs `licetum migrate` exactly once.
#
# Expects: a cluster in the current kube context, the images licetum:ci and licetum-admin:ci loaded into it, helm and kubectl.
#   docker build -f core/Dockerfile --target runtime -t licetum:ci .
#   docker build -f admin-api/Dockerfile --target runtime -t licetum-admin:ci .
#   kind load docker-image licetum:ci licetum-admin:ci
set -euo pipefail

cd "$(dirname "$0")"
ns=${NAMESPACE:-licetum-ci}
release=licetum
chart=licetum
values=(-f $chart/ci/kind-values.yaml)
check=http://$release-check:8001
admin=http://$release-admin:8000

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" apply -f $chart/ci/deps.yaml
kubectl -n "$ns" rollout status deploy/postgres deploy/redis --timeout=180s

log() { printf '\n== %s\n' "$*"; }
curl_pod() { # a command in a pod of the cluster, whose output is the result
  kubectl -n "$ns" run "$1" --rm -i --quiet --restart=Never --image=curlimages/curl:8.10.1 --command -- sh -c "$2"
}

log "install"
helm upgrade --install $release $chart -n "$ns" "${values[@]}" --wait --timeout 6m
kubectl -n "$ns" get pods

log "smoke: the probes, the discovery document, the platform"
curl_pod smoke "set -e
curl -fsS $check/healthz > /dev/null
curl -fsS $check/readyz > /dev/null
curl -fsS $check/.well-known/authzen-configuration/service/licetum | grep -q evaluation
curl -fsS $admin/readyz > /dev/null
curl -fsS $admin/platform | grep -q admin_group_id
echo smoke ok"

migrations() { kubectl -n "$ns" get jobs -l app.kubernetes.io/component=migrate -o json; }
before=$(migrations | python3 -c 'import json,sys; j=json.load(sys.stdin)["items"]; print(len(j), j[0]["metadata"]["uid"] if j else "")')
echo "migration jobs before the upgrade: $before"

log "rolling upgrade under load (about 100 requests a second)"
kubectl -n "$ns" run load --restart=Never --image=curlimages/curl:8.10.1 --command -- sh -c "
for i in \$(seq 1 150); do
  seq 1 100 | xargs -P 25 -I{} sh -c 'curl -s -o /dev/null -w \"%{http_code}\\n\" --max-time 5 $check/healthz || echo curl-failed'
  sleep 0.2
done | sort | uniq -c"
sleep 5
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
helm upgrade $release $chart -n "$ns" "${values[@]}" --set-string check.podAnnotations.rollout="$(date +%s)" --wait --timeout 6m
kubectl -n "$ns" wait --for=jsonpath='{.status.phase}'=Succeeded pod/load --timeout=600s
result=$(kubectl -n "$ns" logs load)
echo "$result"
failed=$(printf '%s\n' "$result" | awk '$2 != "200" { n += $1 } END { print n + 0 }')
if [[ $failed -ne 0 ]]; then
  echo "$failed requests failed during the rolling upgrade" >&2
  exit 1
fi

log "the migration ran exactly once for the upgrade"
JOBS=$(migrations) python3 - "$started" <<'PY'
import json, os, sys
jobs = json.loads(os.environ["JOBS"])["items"]
started = sys.argv[1]
if len(jobs) != 1:
    sys.exit(f"{len(jobs)} migration jobs, want exactly 1")
job = jobs[0]
if job["metadata"]["creationTimestamp"] < started:
    sys.exit("the migration job is older than the upgrade: it did not run for it")
if job["status"].get("succeeded") != 1 or job["spec"].get("completions", 1) != 1:
    sys.exit(f"the migration job did not complete exactly once: {job['status']}")
print("migrate ran once")
PY
log "ok"
