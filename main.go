// Command emapbench measures emap-go against emersion/go-imap v2 on the
// workload emap-go targets: many task-watchers reading from a small set of
// shared inboxes. It runs a localhost IMAP test server (server.go) and drives
// both libraries through it.
//
// Subcommands (one library per process for clean memory isolation):
//
//	emapbench server  <backlog> <statsFile>          # long-lived test server
//	emapbench emap     fanout  <addr> <N>            # N watchers, one inbox
//	emapbench emersion fanout  <addr> <N>            # N clients,  one inbox
//	emapbench emap     startup <addr>                # time/bytes to be ready
//	emapbench emersion startup <addr> <backlog>      # naive full-mailbox sync
package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	emap "github.com/status403com/emap-go"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: emapbench <server|emap|emersion> ...")
	}
	switch os.Args[1] {
	case "server":
		runServer(os.Args[2:])
	case "emap":
		runEmap(os.Args[2:])
	case "emersion":
		runEmersion(os.Args[2:])
	default:
		fail("unknown subcommand %q", os.Args[1])
	}
}

func runServer(args []string) {
	if len(args) < 2 {
		fail("usage: emapbench server <backlog> <statsFile>")
	}
	backlog := atoi(args[0])
	srv, err := newTestServer(backlog)
	if err != nil {
		fail("listen: %v", err)
	}
	srv.statsPath = args[1]
	fmt.Printf("ADDR=%s\n", srv.addr())
	os.Stdout.Sync()
	go srv.flushStatsLoop()
	srv.serve()
}

func credFor(addr string) emap.Credential {
	host, port := splitAddr(addr)
	return emap.Credential{
		Host:     host,
		Port:     port,
		UseTLS:   false,
		Email:    "catchall@example.com",
		Password: "app-password",
	}
}

func runEmap(args []string) {
	if len(args) < 2 {
		fail("usage: emapbench emap <fanout|startup> ...")
	}
	switch args[0] {
	case "fanout":
		emapFanout(args[1], atoi(args[2]))
	case "startup":
		emapStartup(args[1])
	default:
		fail("unknown emap scenario %q", args[0])
	}
}

// emapFanout subscribes N task-watchers to a single shared catch-all inbox.
// All N share ONE pooled IMAP connection and ONE IDLE loop.
func emapFanout(addr string, n int) {
	cred := credFor(addr)
	mgr := emap.NewManager(time.Hour)
	subs := make([]*emap.Subscription, 0, n)
	for i := 0; i < n; i++ {
		f := emap.Filter{To: fmt.Sprintf("task%d@example.com", i)}
		sub, err := mgr.Subscribe(cred, f)
		if err != nil {
			fail("subscribe %d: %v", i, err)
		}
		subs = append(subs, sub)
	}
	// Let the single IDLE loop settle into steady state.
	time.Sleep(300 * time.Millisecond)
	reportMem(len(subs))
	// Hold references so nothing is collected before we measure.
	time.Sleep(200 * time.Millisecond)
	runtime.KeepAlive(subs)
	runtime.KeepAlive(mgr)
}

// emapStartup measures how long emap-go takes to be ready to receive new mail,
// and (via server stats) how many backlog messages it downloads to get there.
// By design: zero — it reads UIDNEXT and never fetches the backlog.
func emapStartup(addr string) {
	cred := credFor(addr)
	mgr := emap.NewManager(time.Hour)
	start := time.Now()
	sub, err := mgr.Subscribe(cred, emap.Filter{To: "task0@example.com"})
	if err != nil {
		fail("subscribe: %v", err)
	}
	elapsed := time.Since(start)
	// Give any (non-existent) startup fetch a chance to land, so the server
	// stats file reflects reality before the driver reads it.
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("ready_ms=%.3f\n", float64(elapsed.Microseconds())/1000.0)
	runtime.KeepAlive(sub)
	runtime.KeepAlive(mgr)
}

func runEmersion(args []string) {
	if len(args) < 2 {
		fail("usage: emapbench emersion <fanout|startup> ...")
	}
	switch args[0] {
	case "fanout":
		emersionFanout(args[1], atoi(args[2]))
	case "startup":
		emersionStartup(args[1])
	default:
		fail("unknown emersion scenario %q", args[0])
	}
}

// emersionFanout is the idiomatic way to watch an inbox per task with a
// low-level client library: one client (one TCP connection, one read loop,
// one IDLE) per task. There is no built-in pooling.
func emersionFanout(addr string, n int) {
	clients := make([]*imapclient.Client, 0, n)
	idles := make([]*imapclient.IdleCommand, 0, n)
	for i := 0; i < n; i++ {
		c, err := imapclient.DialInsecure(addr, nil)
		if err != nil {
			fail("dial %d: %v", i, err)
		}
		if err := c.Login("catchall@example.com", "app-password").Wait(); err != nil {
			fail("login %d: %v", i, err)
		}
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			fail("select %d: %v", i, err)
		}
		idle, err := c.Idle()
		if err != nil {
			fail("idle %d: %v", i, err)
		}
		clients = append(clients, c)
		idles = append(idles, idle)
	}
	time.Sleep(300 * time.Millisecond)
	reportMem(len(clients))
	time.Sleep(200 * time.Millisecond)
	runtime.KeepAlive(clients)
	runtime.KeepAlive(idles)
}

// emersionStartup is the naive-but-common "catch up on the mailbox" sync: after
// SELECT, enumerate the mailbox to find recent mail. It pays a per-message cost
// that scales with backlog size. (A careful user could instead read UIDNext and
// fetch nothing — exactly what emap-go does automatically.)
func emersionStartup(addr string) {
	start := time.Now()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		fail("dial: %v", err)
	}
	if err := c.Login("catchall@example.com", "app-password").Wait(); err != nil {
		fail("login: %v", err)
	}
	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		fail("select: %v", err)
	}
	if sel.NumMessages > 0 {
		var set imap.SeqSet
		set.AddRange(1, 0) // 1:* — the whole mailbox
		opts := &imap.FetchOptions{UID: true, Flags: true, InternalDate: true, RFC822Size: true}
		if _, err := c.Fetch(set, opts).Collect(); err != nil {
			fail("fetch: %v", err)
		}
	}
	elapsed := time.Since(start)
	time.Sleep(200 * time.Millisecond)
	fmt.Printf("ready_ms=%.3f exists=%d\n", float64(elapsed.Microseconds())/1000.0, sel.NumMessages)
	runtime.KeepAlive(c)
}

// reportMem prints the current process footprint: resident memory, live heap,
// and goroutine count, after forcing GC so the numbers reflect retained state.
func reportMem(n int) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rss := vmRSSKiB()
	fmt.Printf("n=%d rss_kb=%d heap_kb=%d goroutines=%d\n",
		n, rss, ms.HeapInuse/1024, runtime.NumGoroutine())
}

// vmRSSKiB reads resident set size (KiB) from /proc/self/status.
func vmRSSKiB() int64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return -1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if len(line) > 6 && line[:6] == "VmRSS:" {
			var v int64
			fmt.Sscanf(line, "VmRSS: %d kB", &v)
			return v
		}
	}
	return -1
}

func splitAddr(addr string) (host string, port int) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			port = atoi(addr[i+1:])
			return host, port
		}
	}
	return addr, 0
}

func atoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		fail("bad int %q: %v", s, err)
	}
	return v
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
