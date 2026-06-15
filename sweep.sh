#!/usr/bin/env bash
# Sweeps both benchmark scenarios for emap-go and emersion/go-imap v2, one OS
# process per measurement (clean memory isolation), and writes CSV results.
#
#   ./sweep.sh                  # builds ./emapbench, writes ./results/*.csv
#
# Then regenerate the charts from the fresh data:
#
#   node charts/gen.mjs
set -u
cd "$(dirname "$0")"

BIN=./emapbench
echo "building $BIN ..."
go build -o "$BIN" . || { echo "build failed"; exit 1; }

OUT=./results
mkdir -p "$OUT"
FAN="$OUT/fanout.csv"
STA="$OUT/startup.csv"
echo "lib,n,accepts,rss_kb,heap_kb,goroutines" > "$FAN"
echo "lib,backlog,ready_ms,fetchmsgs,bytes" > "$STA"

start_server() {  # $1=backlog -> echoes "addr pid stats"
  local backlog=$1
  local stats sout; stats=$(mktemp); sout=$(mktemp)
  $BIN server "$backlog" "$stats" >"$sout" 2>/dev/null &
  local spid=$!
  local addr=""
  for _ in $(seq 1 100); do
    addr=$(grep -m1 '^ADDR=' "$sout" 2>/dev/null | cut -d= -f2)
    [ -n "$addr" ] && break
    sleep 0.03
  done
  echo "$addr $spid $stats"
}
stat_val() { grep -o "$2=[0-9]*" "$1" 2>/dev/null | head -1 | cut -d= -f2; }
cli_val()  { grep -o "$2=[0-9.]*" <<<"$1" | head -1 | cut -d= -f2; }

echo "### fanout sweep (connections + memory vs N watchers on one inbox)"
for n in 1 5 15 50 100 250 500 1000 2000; do
  for lib in emap emersion; do
    read -r addr spid stats < <(start_server 0)
    [ -z "$addr" ] && { echo "  $lib n=$n: server fail"; kill -9 "$spid" 2>/dev/null; continue; }
    line=$($BIN "$lib" fanout "$addr" "$n" 2>/dev/null); sleep 0.12
    echo "$lib,$n,$(stat_val "$stats" accepts),$(cli_val "$line" rss_kb),$(cli_val "$line" heap_kb),$(cli_val "$line" goroutines)" | tee -a "$FAN"
    kill -9 "$spid" 2>/dev/null; rm -f "$stats"
  done
done

echo "### startup sweep (time + volume to be ready vs inbox backlog)"
for backlog in 0 1000 10000 100000 1000000; do
  for lib in emap emersion; do
    read -r addr spid stats < <(start_server "$backlog")
    [ -z "$addr" ] && { echo "  $lib b=$backlog: server fail"; kill -9 "$spid" 2>/dev/null; continue; }
    if [ "$lib" = emap ]; then line=$($BIN emap startup "$addr" 2>/dev/null)
    else line=$($BIN emersion startup "$addr" 2>/dev/null); fi
    sleep 0.15
    echo "$lib,$backlog,$(cli_val "$line" ready_ms),$(stat_val "$stats" fetchmsgs),$(stat_val "$stats" bytes)" | tee -a "$STA"
    kill -9 "$spid" 2>/dev/null; rm -f "$stats"
  done
done

pkill -9 -f "$BIN server" 2>/dev/null
echo "done -> $FAN  $STA"
