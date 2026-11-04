#!/bin/sh
# restart.sh <container>: restart the node, wait for CQL, drop the OS page cache (needs root on the host)
docker restart "$1" >/dev/null
i=0
until docker exec "$1" cqlsh -e "SELECT now() FROM system.local" >/dev/null 2>&1; do
  i=$((i+1)); [ $i -gt 300 ] && { echo "not ready"; exit 1; }; sleep 1
done
sleep 5
sync; echo 3 > /proc/sys/vm/drop_caches && echo "page cache dropped"
