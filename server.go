package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// testServer is a minimal IMAP server used only for benchmarking. It speaks
// just enough of RFC 3501/9051 to drive both emap-go and emersion/go-imap v2
// through connect -> LOGIN -> CAPABILITY -> SELECT -> IDLE/FETCH -> LOGOUT.
//
// It is deliberately NOT a correct IMAP server: it exists to make connection
// counts, memory, and fetch volume measurable and reproducible on localhost,
// with zero external services.
type testServer struct {
	ln      net.Listener
	backlog int // number of pre-existing messages in INBOX (drives EXISTS/UIDNEXT)

	accepts   atomic.Int64 // total TCP connections accepted
	fetchMsgs atomic.Int64 // total `* n FETCH` lines emitted across all conns
	bytesOut  atomic.Int64 // total bytes written to clients

	statsPath string // if set, stats are flushed here every 50ms
}

// uidNext is one past the last backlog UID. emap-go reads this from SELECT and
// uses it to skip the backlog entirely (it only ever FETCHes UID >= uidNext).
func (s *testServer) uidNext() uint32 { return uint32(s.backlog) + 1 }

func newTestServer(backlog int) (*testServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &testServer{ln: ln, backlog: backlog}, nil
}

func (s *testServer) addr() string { return s.ln.Addr().String() }

func (s *testServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.accepts.Add(1)
		go s.handle(c)
	}
}

// countingWriter tallies bytes written so the startup benchmark can report the
// real wire volume each library pulls down.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (cw countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n.Add(int64(n))
	return n, err
}

func (s *testServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(countingWriter{w: c, n: &s.bytesOut})

	const caps = "IMAP4rev1 IMAP4rev2 IDLE LITERAL+ UIDPLUS AUTH=PLAIN"
	fmt.Fprintf(w, "* OK [CAPABILITY %s] emapbench test server ready\r\n", caps)
	w.Flush()

	for {
		line, err := readCommand(r, w)
		if err != nil {
			return
		}
		tag, cmd, args := splitCommand(line)
		if tag == "" {
			continue
		}
		switch cmd {
		case "CAPABILITY":
			fmt.Fprintf(w, "* CAPABILITY %s\r\n", caps)
			fmt.Fprintf(w, "%s OK CAPABILITY completed\r\n", tag)
		case "LOGIN", "AUTHENTICATE":
			fmt.Fprintf(w, "%s OK [CAPABILITY %s] LOGIN completed\r\n", tag, caps)
		case "ENABLE":
			fmt.Fprintf(w, "* ENABLED IMAP4rev2\r\n")
			fmt.Fprintf(w, "%s OK ENABLE completed\r\n", tag)
		case "SELECT", "EXAMINE":
			s.writeSelect(w, tag)
		case "NOOP", "CHECK":
			fmt.Fprintf(w, "%s OK %s completed\r\n", tag, cmd)
		case "IDLE":
			if !s.handleIdle(r, w, tag) {
				return
			}
		case "FETCH", "UID":
			// "UID" => "UID FETCH <set> (...)". Normalize to a fetch.
			s.handleFetch(w, tag, cmd, args)
		case "LOGOUT":
			fmt.Fprintf(w, "* BYE logging out\r\n%s OK LOGOUT completed\r\n", tag)
			w.Flush()
			return
		default:
			fmt.Fprintf(w, "%s OK %s completed\r\n", tag, cmd)
		}
		w.Flush()
	}
}

func (s *testServer) writeSelect(w *bufio.Writer, tag string) {
	fmt.Fprintf(w, "* FLAGS (\\Seen \\Answered \\Flagged \\Deleted \\Draft)\r\n")
	fmt.Fprintf(w, "* %d EXISTS\r\n", s.backlog)
	fmt.Fprintf(w, "* OK [PERMANENTFLAGS (\\Seen \\Answered \\Flagged \\Deleted \\Draft \\*)] permitted\r\n")
	fmt.Fprintf(w, "* OK [UIDVALIDITY 1] UIDs valid\r\n")
	fmt.Fprintf(w, "* OK [UIDNEXT %d] Predicted next UID\r\n", s.uidNext())
	fmt.Fprintf(w, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
}

// handleIdle replies with the IDLE continuation and blocks reading until DONE.
// Returns false if the connection died (caller should stop).
func (s *testServer) handleIdle(r *bufio.Reader, w *bufio.Writer, tag string) bool {
	fmt.Fprintf(w, "+ idling\r\n")
	w.Flush()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return false
		}
		if strings.EqualFold(strings.TrimRight(line, "\r\n"), "DONE") {
			fmt.Fprintf(w, "%s OK IDLE terminated\r\n", tag)
			return true
		}
	}
}

// handleFetch parses the message set and emits one FETCH response per message
// in range. emap-go asks for header+text literals; emersion asks for
// UID/FLAGS/INTERNALDATE/RFC822.SIZE atoms. We detect which by the request.
func (s *testServer) handleFetch(w *bufio.Writer, tag, cmd, args string) {
	isUID := cmd == "UID"
	set := args
	if isUID {
		// args = "FETCH <set> (...)"
		set = strings.TrimSpace(strings.TrimPrefix(args, "FETCH"))
	}
	lo, hi := parseMsgSet(set, int(s.uidNext())-1)
	if lo < 1 {
		lo = 1
	}
	maxID := s.backlog
	if hi > maxID {
		hi = maxID
	}
	wantBody := strings.Contains(strings.ToUpper(args), "BODY")
	for id := lo; id <= hi; id++ {
		if wantBody {
			s.writeBodyFetch(w, id)
		} else {
			s.writeAtomFetch(w, id)
		}
		s.fetchMsgs.Add(1)
	}
	fmt.Fprintf(w, "%s OK FETCH completed\r\n", tag)
}

const internalDate = "01-Jan-2026 12:00:00 +0000"

// writeBodyFetch emits the header-fields + text literal shape emap-go parses.
func (s *testServer) writeBodyFetch(w *bufio.Writer, id int) {
	headers := fmt.Sprintf("To: task%d@example.com\r\nFrom: no-reply@vendor.com\r\nSubject: Your code\r\nDate: Wed, 01 Jan 2026 12:00:00 +0000\r\n\r\n", id)
	body := fmt.Sprintf("Your verification code is %06d\r\n", id%1000000)
	fmt.Fprintf(w, "* %d FETCH (UID %d INTERNALDATE %q BODY[HEADER.FIELDS (TO FROM SUBJECT DATE)] {%d}\r\n",
		id, id, internalDate, len(headers))
	w.WriteString(headers)
	fmt.Fprintf(w, " BODY[TEXT] {%d}\r\n", len(body))
	w.WriteString(body)
	w.WriteString(")\r\n")
}

// writeAtomFetch emits a lightweight atom-only response (no bodies) — the
// cheapest realistic shape an emersion "list the mailbox" sync would request.
func (s *testServer) writeAtomFetch(w *bufio.Writer, id int) {
	fmt.Fprintf(w, "* %d FETCH (UID %d FLAGS (\\Seen) INTERNALDATE %q RFC822.SIZE %d)\r\n",
		id, id, internalDate, 2048)
}

func (s *testServer) flushStatsLoop() {
	if s.statsPath == "" {
		return
	}
	for {
		s.writeStats()
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *testServer) writeStats() {
	data := fmt.Sprintf("accepts=%d fetchmsgs=%d bytes=%d\n",
		s.accepts.Load(), s.fetchMsgs.Load(), s.bytesOut.Load())
	_ = os.WriteFile(s.statsPath, []byte(data), 0o644)
}

// --- wire parsing helpers ---

// readCommand reads one logical command line, inlining any LITERAL+/literal
// continuations so callers see a single string. Mirrors how a real server
// reassembles `{n}`-delimited arguments.
func readCommand(r *bufio.Reader, w *bufio.Writer) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	for {
		n, nonSync, ok := trailingLiteral(line)
		if !ok {
			return line, nil
		}
		if !nonSync {
			w.WriteString("+ OK\r\n")
			w.Flush()
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		rest, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = line + string(buf) + strings.TrimRight(rest, "\r\n")
	}
}

// trailingLiteral reports whether line ends with `{n}` (sync) or `{n+}`
// (LITERAL+, non-sync).
func trailingLiteral(line string) (n int, nonSync bool, ok bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false, false
	}
	open := strings.LastIndexByte(line, '{')
	if open < 0 {
		return 0, false, false
	}
	inner := line[open+1 : len(line)-1]
	if strings.HasSuffix(inner, "+") {
		nonSync = true
		inner = inner[:len(inner)-1]
	}
	v, err := strconv.Atoi(inner)
	if err != nil {
		return 0, false, false
	}
	return v, nonSync, true
}

func splitCommand(line string) (tag, cmd, args string) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) < 2 {
		return "", "", ""
	}
	tag = fields[0]
	cmd = strings.ToUpper(fields[1])
	if len(fields) == 3 {
		args = fields[2]
	}
	return tag, cmd, args
}

// parseMsgSet handles the simple sets the benchmark uses: "1:*", "5:*", "1:50",
// "42". star resolves to starVal.
func parseMsgSet(set string, starVal int) (lo, hi int) {
	set = strings.TrimSpace(set)
	// strip a trailing " (...)" if the set token wasn't split cleanly
	if i := strings.IndexByte(set, ' '); i >= 0 {
		set = set[:i]
	}
	if set == "" {
		return 1, starVal
	}
	parts := strings.SplitN(set, ":", 2)
	lo = atoiStar(parts[0], starVal)
	if len(parts) == 1 {
		return lo, lo
	}
	hi = atoiStar(parts[1], starVal)
	return lo, hi
}

func atoiStar(s string, starVal int) int {
	if s == "*" {
		return starVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return starVal
	}
	return v
}
