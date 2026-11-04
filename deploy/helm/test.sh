#!/usr/bin/env bash
# The chart tests that need no cluster (F6-12): helm lint, helm template compared with the golden files, kubeconform.
#
#   deploy/helm/test.sh            check
#   deploy/helm/test.sh update     rewrite the golden files after an intended change of the chart
#
# Needs helm and kubeconform on the PATH. The kind installation test is deploy/helm/kind-test.sh.
set -euo pipefail

cd "$(dirname "$0")"
mode=${1:-check}
kube=${KUBE_VERSION:-1.30.0}
chart=licetum
golden=$chart/tests/golden

# name -> helm arguments of the render (the values of chart/ci/, the cell profile on top of the defaults)
declare -A renders=(
  [default]="-f $chart/ci/default-values.yaml"
  [full]="-f $chart/ci/full-values.yaml"
  [cell]="-f $chart/values-cell.yaml -f $chart/ci/cell-values.yaml"
)

status=0
for name in "${!renders[@]}"; do
  # shellcheck disable=SC2086
  helm lint $chart ${renders[$name]} > /dev/null
  # shellcheck disable=SC2086
  out=$(helm template release $chart --kube-version "$kube" ${renders[$name]})
  kubeconform -strict -summary -ignore-missing-schemas -kubernetes-version "$kube" <<< "$out" > /dev/null
  if [[ $mode == update ]]; then
    mkdir -p "$golden"
    printf '%s\n' "$out" > "$golden/$name.yaml"
    echo "updated $golden/$name.yaml"
  elif ! diff -u "$golden/$name.yaml" <(printf '%s\n' "$out"); then
    echo "the render of '$name' differs from $golden/$name.yaml (deploy/helm/test.sh update, if intended)" >&2
    status=1
  fi
done

# A release without the required secrets must not render.
if helm template release $chart --kube-version "$kube" > /dev/null 2>&1; then
  echo "the chart rendered without secrets.dbUri / existingSecret" >&2
  status=1
fi

# The cell profile: the network policy names the cell's own PostgreSQL and nothing else (F6-10).
cell=$(helm template release $chart --kube-version "$kube" -f $chart/values-cell.yaml -f $chart/ci/cell-values.yaml)
for cidr in $(grep -oE 'cidr: "?[0-9./]+"?' <<< "$cell" | tr -d '"' | awk '{print $2}' | sort -u); do
  case $cidr in
    10.30.0.0/24 | 10.30.1.0/24 | 0.0.0.0/0) ;;
    *) echo "the cell's network policy reaches $cidr, outside the cell" >&2; status=1 ;;
  esac
done

exit $status
