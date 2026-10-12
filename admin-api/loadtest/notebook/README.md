# Load test results and how to get the same charts on your machine

This directory holds the charts of the check API load test: `licetum-loadtest.ipynb` draws them from the
`result.json` and `environment.json` files of the runs. The runs themselves are made by the harness in
[`../`](..), which this README also describes.

The numbers depend on the machine. The published runs (`results/`, when present) are from one machine, and the
notebook shows the machine each run came from. To get the same charts for your own hardware, follow the steps below.

## What is here

| path | what it is |
|---|---|
| `licetum-loadtest.ipynb` | the charts; reads the directory named by `LICETUM_RESULTS` |
| `requirements.txt` | the Python packages the notebook needs |
| `results/` | the runs the notebook is built from: `result.json` and `environment.json` per run (see below) |
| [`../compose.yml`](../compose.yml) | PostgreSQL 16 and Redis 7 for the runs, on the loopback interface |
| [`../scripts/baseline.sh`](../scripts/baseline.sh) | the campaign: five runs per mode, a ramp, and the aggregate report |

## Published results (preliminary)

`results/` holds two series of the smoke profile (10k resources), warm mode, 200 requests/s. Each run is a directory with its
`result.json` and `environment.json`; nothing else is published.

- `clean-0ff9a6b7/smoke-warm/run-1..4`: commit `0ff9a6b7`, a clean tree, the `performance` governor, no swap. Runs 1
  and 2 are valid. Run 3 is invalid because the host swapped memory during it. Run 4 is invalid because it was stopped
  by hand.
- `dirty-3cd0b4c3/smoke-warm/run-1..5`: the first attempt, with uncommitted changes, the `powersave` governor and swap in
  use. All five are valid by the rules, but they are here to show how far the state of the host moves the same code, not
  as a result.

Not measured yet: the 100k profile, the mixed, cold and ramp modes, writes during load, faults, and a second machine. Two
valid windows on one laptop are not enough for an article's numbers.

## 1. Requirements

- **Linux** with `taskset`, to pin the load generator and the check server to their own CPUs. Without it, set
  `K6_CPUS=` and `CHECK_CPUS=` to empty; the runs are then not pinned, and `environment.json` says so.
- **Go** as in `admin-api/go.mod` (1.26).
- **Docker** with Compose v2, for the PostgreSQL and Redis containers.
- **k6** v2 (the version is recorded in `environment.json`). See
  [the k6 installation guide](https://grafana.com/docs/k6/latest/set-up/install-k6/).
- **Python 3.11** or newer, for the notebook.

Give the machine nothing else to do while a campaign runs. A browser, an IDE that indexes, or a virtual machine on the
same cores moves the numbers, and the harness does not see them (see [Limitations](#limitations)).

## 2. Start PostgreSQL and Redis

```sh
docker compose -f admin-api/loadtest/compose.yml up -d
export LICETUM_TEST_POSTGRES_URI="postgres://licetum:licetum@127.0.0.1:55432/postgres?sslmode=disable"
export LICETUM_TEST_REDIS_URI="redis://127.0.0.1:56379/0"
```

The compose file sets the PostgreSQL password to `licetum` unless `LT_PG_PASSWORD` is set; if you set it, put the same
value in `LICETUM_TEST_POSTGRES_URI`. The servers listen on `127.0.0.1` only. The harness creates a database of its own
for each run and drops it afterwards.

## 3. Build and check one window

From the repository root:

```sh
task loadtest:build                                  # core/bin/licetum: the binary that is measured
cd admin-api
go build -o ../core/bin/licetum-loadtest ./cmd/licetum-loadtest
../core/bin/licetum-loadtest run -profile smoke -mode warm -rate 200 -warm 60s -measure 2m \
  -check-bin ../core/bin/licetum -k6-bin k6 -k6-cpus 10-11 -check-cpus 0-7 -out reports/try
```

`-profile` picks the size of the policy: `smoke` is 10,000 resources, `medium` 100,000, `500k` 500,000 and `large`
1,000,000. `-users`, `-groups`, `-rules` and `-resources` override one value of the profile. `-mode` is `warm` (the
caches answer), `cold` (every request is computed) or `mix` (skewed traffic). `-k6-cpus` and `-check-cpus` take the
CPU lists of your machine (`nproc`, `lscpu -e`): the check server, the load generator and the stack must not share a
CPU. The stack runs on CPUs 8-9 (`cpuset` in `compose.yml`); `environment.json` records its CPU sets.

A window is valid when no request was dropped, the load generator stayed under 70 % of its CPUs and the oracle found no
wrong answer. An invalid window is written with its reasons; it is not hidden.

## 4. The campaign

```sh
cd admin-api
go build -o ../core/bin/licetum-loadtest ./cmd/licetum-loadtest
LT=../core/bin/licetum-loadtest K6=k6 OUT=reports/mine PROFILE=smoke \
  K6_CPUS=10-11 CHECK_CPUS=0-7 CHECK_BIN=../core/bin/licetum scripts/baseline.sh
```

The campaign runs, for the `smoke` profile at its default settings:

- five fixed-rate runs in `warm` mode (200 requests/s), five in `mix` (200/s), five in `cold` (100/s), each with a
  60-second warm-up and a 10-minute measured window;
- one ramp from 50 requests/s, doubling each two-minute step, until a step misses its objectives;
- the aggregate report (`reports/mine/smoke-report.md`).

Every run starts on a fresh stack: the script restarts the `lt-pg` and `lt-redis` containers, so the buffer cache and
Redis are empty, and then seeds the policy again. On the test host the campaign takes about three hours. Set
`MEASURE=120s` for a first pass: it takes about an hour, the ramp included.

Use the same CPU lists, the same profile and the same settings for every run you compare.

## 5. Get the charts

```sh
cd admin-api/loadtest/notebook
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
LICETUM_RESULTS="$(cd ../../ && pwd)/reports/mine" .venv/bin/jupyter lab licetum-loadtest.ipynb
```

Then run all cells. To write the charts to a file without opening Jupyter:

```sh
LICETUM_RESULTS="$(cd ../../ && pwd)/reports/mine" .venv/bin/jupyter nbconvert --to html --execute \
  --output-dir ../../reports/mine --output charts licetum-loadtest.ipynb
```

The notebook reads every `result.json` under `LICETUM_RESULTS`, so one directory can hold the runs of several
profiles, modes and ramps. It prints only relative paths.

## 6. Read your numbers

- **Compare inside one environment.** The notebook's environment table lists the CPU, kernel, governor, Git commit,
  PostgreSQL version and CPU sets of the runs. Numbers from different rows are not comparable.
- **Check the validity first.** A window with dropped requests, a busy generator or an oracle mismatch is not a result.
- **Spread above 15 %** means the runs of one configuration were in different states: a change of the CPU frequency, a
  background process, a neighbour on the host. Find the cause before you average the runs. On Linux,
  `sudo cpupower frequency-set -g performance` sets the governor for the campaign; close the other programs first.
- **Commit only from a clean tree.** `environment.json` records `git_dirty`. A run from uncommitted code is useful for
  development, not for a published number.
- **Publish a subset, not the output.** `reports/` is ignored by Git. To publish runs, copy their `result.json`,
  `environment.json` and `summary.md` into `admin-api/loadtest/notebook/results/<name>/`. The time series and the k6
  logs stay local.

## Limitations

- One machine, one database, the check server as one process (or the `-replicas` you name), and the load generator on
  the CPUs pinned to it. The numbers describe this setup, not a deployment.
- The policy is generated: its shape (owner rules, group and department grants, denials with exceptions) is the shape
  the harness builds. The oracle checks the answers against that shape only.
- Warm-up is 60 seconds. The first synchronization of a service happens before the measured window. The cold mode
  computes every request; it does not measure the first request after a restart.
- The harness samples the server, PostgreSQL and its own process. It does not sample the host: other load on the
  machine, CPU frequency or the steal time of a virtual machine show up only as a change of latency, not as an
  explanation of it.
- The notebook shows what the runs contain. It does not judge a configuration beyond the validity and the spread.
