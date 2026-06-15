# emap-go-bench

Reproducible, head-to-head benchmarks for [`emap-go`](https://github.com/status403com/emap-go)
against [`emersion/go-imap`](https://github.com/emersion/go-imap) v2 (`imapclient`),
on the workload `emap-go` is built for: **many task-watchers reading mail from a
small set of shared inboxes** (catch-all verification flows, automation, bots).

Everything runs against a tiny localhost IMAP server included here
([`server.go`](server.go)) — no external services, no real mailboxes, no network
flakiness. You can run the whole thing in under a minute and check the numbers
yourself.

## What this is (and isn't)

`emap-go` and `emersion/go-imap` are **not the same kind of thing**, and the
benchmarks are framed honestly around that:

- `emersion/go-imap` is a general-purpose, RFC-complete IMAP toolkit (client
  **and** server). It is excellent and does far more than `emap-go`.
- `emap-go` is a narrow, opinionated **connection manager**: one pooled
  connection per credential, fanned out to N subscribers, with IDLE, backoff
  reconnect, and backlog-skip baked in.

So the comparison is **"what does it cost to build an N-task mail-watcher?"**
With `emap-go` you get pooling/fanout/backlog-skip for free. With a low-level
client the idiomatic answer is one client per task — which is what we measure.
Where a careful `emersion` user could match `emap-go` (e.g. reading `UIDNEXT`
manually instead of syncing the mailbox), that's called out explicitly below.

## Run it

```bash
# clone both repos side by side (go.mod uses a replace directive)
git clone https://github.com/status403com/emap-go
git clone https://github.com/status403com/emap-go-bench
cd emap-go-bench

./sweep.sh              # writes results/fanout.csv and results/startup.csv
node charts/gen.mjs     # regenerates charts/*.svg from the CSVs
```

You can also drive a single measurement by hand:

```bash
go build -o emapbench .
./emapbench server 100000 /tmp/stats &      # backlog of 100k messages
./emapbench emersion startup 127.0.0.1:PORT # naive mailbox sync
cat /tmp/stats                              # fetchmsgs / bytes the client pulled
```

## Methodology

- **One OS process per measurement.** The server runs in its own process; each
  client run is its own process. Memory (`VmRSS` from `/proc/self/status`) and
  goroutine counts therefore reflect the client library alone, after a forced
  `runtime.GC()`.
- **Connections are counted server-side** (every `accept()` is tallied), so the
  fanout numbers don't depend on trusting either client.
- **Startup volume is counted server-side** too: the exact number of `FETCH`
  responses emitted and bytes written on the wire.
- Same machine, plain TCP on `127.0.0.1`, Go 1.26, 16 cores.

## Results (sample run)

### Fanout — N task-watchers on one shared inbox

| N | emap-go conns | emersion conns | emap-go RSS | emersion RSS | emap-go goroutines | emersion goroutines |
|---|---|---|---|---|---|---|
| 1 | 1 | 1 | 7.0 MB | 7.0 MB | 3 | 3 |
| 15 | **1** | 15 | 7.0 MB | 7.8 MB | **3** | 31 |
| 100 | **1** | 100 | 7.5 MB | 9.8 MB | **3** | 201 |
| 1000 | **1** | 1000 | 8.8 MB | 27.6 MB | **3** | 2001 |
| 2000 | **1** | 2000 | **10.4 MB** | **48.9 MB** | **3** | 4001 |

`emap-go` holds **one** connection and **three** goroutines no matter how many
tasks subscribe. The per-task client model scales connections, goroutines, and
memory linearly — and blows past Gmail's 15-connections-per-account cap at N=16.

### Startup — cost to be ready for new mail vs. inbox backlog

| backlog | emap-go ready | emap-go fetched | emersion ready | emersion fetched | emersion bytes |
|---|---|---|---|---|---|
| 1k | 1.4 ms | **0 msgs** | 5.7 ms | 1,000 msgs | 96 KB |
| 10k | 1.6 ms | **0 msgs** | 31 ms | 10,000 msgs | 978 KB |
| 100k | 1.5 ms | **0 msgs** | 254 ms | 100,000 msgs | 9.98 MB |
| 1M | 0.9 ms | **0 msgs** | 2,548 ms | 1,000,000 msgs | 101.8 MB |

`emap-go` reads `UIDNEXT` from `SELECT` and only ever fetches UIDs above it, so
startup is **constant** regardless of how much mail is already in the box. A
naive "sync the mailbox" startup pays linearly — 2.4 s and ~102 MB on a 1M-message
inbox. (A careful `emersion` user can read `UIDNext` too and skip the sync; the
point is `emap-go` does it for you, by default.)

### Supply chain

| | emap-go | emersion go-imap v2 + imapclient |
|---|---|---|
| external modules in build | **0** | 13 |
| `go.sum` entries | **0** | 37 |
| source | ~1,600 lines, one package, stdlib only | larger, multi-module |

(Static binary sizes are comparable, ~4 MB each — this is about dependency
surface and auditability, not binary size.)

## Files

- [`server.go`](server.go) — minimal localhost IMAP test server
- [`main.go`](main.go) — benchmark scenarios (`fanout`, `startup`) for both libs
- [`sweep.sh`](sweep.sh) — runs the full sweep, writes `results/*.csv`
- [`charts/gen.mjs`](charts/gen.mjs) — renders the SVG charts from the CSVs
- [`results/`](results) — sample CSV output
- [`charts/`](charts) — sample rendered charts

## License

MIT — see [LICENSE](LICENSE).
