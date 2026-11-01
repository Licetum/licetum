# Load test budgets

**Within the budgets.** A figure fails when it is more than 15 % on the wrong side of its budget (a latency also gets the slack of the budgets file).

| configuration | figure | budget | measured | verdict | detail |
|---|---|---:|---:|---|---|
| medium/mix/+0ms | sustained rps | 80.00 | 80.01 | ok |  |
| medium/mix/+0ms | p50 ms | 7.48 | 10.69 | ok |  |
| medium/mix/+0ms | p99 ms | – | – | skipped | no budget |
| medium/mix/+0ms | db queries per request | 6.28 | 6.54 | ok |  |

