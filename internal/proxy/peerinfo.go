package proxy

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PeerInfo describes the local process that owns the other end of a
// TCP connection charon accepted. Display-quality only — the spec
// (#16 §3) is explicit: never gate auth on this. The connecting
// process can fork/exec between accept and lookup; the snapshot is
// observed-at-accept, not authoritative-at-attribution.
//
// All fields are best-effort. Missing fields render as zero values
// in JSON; lookups that fail entirely return a nil *PeerInfo.
type PeerInfo struct {
	PID         int         `json:"pid"`
	Exe         string      `json:"exe,omitempty"`
	Argv0       string      `json:"argv0,omitempty"`
	ParentChain []ParentRef `json:"parent_chain,omitempty"`
}

// ParentRef is one entry in the parent-chain walk. PID 1 (launchd)
// is the typical terminal of the chain. Walked once at CONNECT;
// process tree changes after that aren't reflected.
type ParentRef struct {
	PID int    `json:"pid"`
	Exe string `json:"exe,omitempty"`
}

// ResolvePeer finds the local process owning the TCP connection that
// opened the given local-source-port `peerPort` against charon's
// listener. Returns nil on any lookup failure — callers tolerate
// nil and log "unknown" rather than error out.
//
// Implementation: shells out to `lsof -iTCP:<port> -sTCP:ESTABLISHED`
// and `ps -o ppid=,comm=`. Per-CONNECT one-shot, ~50ms worst-case;
// amortized over a long-lived tunnel that costs is fine. The spec's
// alternate path (proc_listpids + proc_pidinfo via CGo) would be
// faster but adds ~200 lines of CGo for a 50ms savings on a path
// that already pays a TLS-handshake cost.
//
// "Peer port" semantics: when an agent connects to charon's
// 127.0.0.1:8230, charon sees the connection as having a remote
// address of `127.0.0.1:<ephemeral>`. That ephemeral port is the
// agent's local source port; passing it here resolves which agent
// process owns it.
func ResolvePeer(peerPort int) *PeerInfo {
	pid := lookupPIDForPort(peerPort)
	if pid == 0 {
		return nil
	}
	info := &PeerInfo{PID: pid}
	if exe := readPidExe(pid); exe != "" {
		info.Exe = exe
	}
	if argv0 := readPidComm(pid); argv0 != "" {
		info.Argv0 = argv0
	}
	info.ParentChain = walkParentChain(pid)
	return info
}

// lookupPIDForPort runs lsof to find the process whose LOCAL TCP port
// matches peerPort. The proxy's listener also has a socket touching
// this port (REMOTE side), so we filter out our own pid before
// returning. Returns 0 on no match or any parse failure.
func lookupPIDForPort(peerPort int) int {
	if peerPort <= 0 {
		return 0
	}
	// -F np: parseable output, only n (NAME) and p (PID) fields per
	// record. Records are separated by `p<pid>` prefix lines.
	out, err := exec.Command("lsof",
		"-nP",                   // numeric, no DNS / port-name lookups
		"-iTCP:"+strconv.Itoa(peerPort),
		"-sTCP:ESTABLISHED",
		"-Fpn",                  // parseable: emit p (pid) and n (name)
	).Output()
	if err != nil {
		return 0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var curPid int
	want := fmt.Sprintf(":%d->", peerPort) // local-side match marker
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ := strconv.Atoi(line[1:])
			curPid = pid
		case 'n':
			// NAME field: e.g. "127.0.0.1:60123->127.0.0.1:8230".
			// `:peerPort->` marker matches only the side where
			// peerPort is the LOCAL (left) port — i.e. the connecting
			// agent's socket, not the proxy's accepted-side socket
			// where peerPort would appear as the REMOTE.
			if curPid != 0 && strings.Contains(line, want) {
				return curPid
			}
		}
	}
	return 0
}

// readPidExe returns the absolute executable path for pid, or "" on
// failure. macOS doesn't expose this through /proc; we shell out to
// `ps -o comm=` which prints the executable path for the process.
// (`comm=` is documented to print the long argv0 / executable on
// modern BSD ps; Linux ps would need `args` for the equivalent.)
func readPidExe(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readPidComm returns the short process name (no path), useful for
// human display when the full exe path is too long. Falls back to
// the basename of readPidExe when ps doesn't separate them.
func readPidComm(pid int) string {
	exe := readPidExe(pid)
	if exe == "" {
		return ""
	}
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		return exe[i+1:]
	}
	return exe
}

// walkParentChain walks up from pid via ppid until it reaches PID 1
// (launchd) or the chain breaks. Bounded at 16 levels to defend
// against pathological process trees / lookup loops. Each entry
// includes the pid + exe; failures partway up truncate the chain
// rather than abort.
func walkParentChain(pid int) []ParentRef {
	chain := make([]ParentRef, 0, 4)
	cur := pid
	for i := 0; i < 16; i++ {
		ppid := readPpid(cur)
		if ppid <= 0 || ppid == cur {
			break
		}
		ref := ParentRef{PID: ppid}
		if exe := readPidExe(ppid); exe != "" {
			ref.Exe = exe
		}
		chain = append(chain, ref)
		if ppid == 1 {
			break
		}
		cur = ppid
	}
	return chain
}

// readPpid returns pid's parent pid, or 0 on failure.
func readPpid(pid int) int {
	if pid <= 0 {
		return 0
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}
