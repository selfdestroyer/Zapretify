// Package macdaemon is the root helper of the macOS port (zapretifyd) and its client.
//
// The daemon runs from launchd as root and is the only part that touches pf and starts utunws.
// The application talks to it over a Unix socket: one JSON request, one JSON response per connection.
// Only the user who installed the daemon (and root) may send commands.
package macdaemon

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	// SocketPath is where zapretifyd listens.
	SocketPath = "/var/run/zapretifyd.sock"
	// RootDir holds everything the daemon runs from (root:wheel); the user cannot change it.
	RootDir = "/Library/Application Support/Zapretify"
	// Label is the launchd label of the daemon.
	Label = "io.github.selfdestroyer.zapretifyd"
	// UserDataSubdir is the application's data directory inside the user's home;
	// the daemon imports strategies.json, lists/ and bin/*.bin from there.
	UserDataSubdir = "Library/Application Support/Zapretify"

	// Version of the request/response format; bumped on incompatible changes.
	ProtocolVersion = 1
)

// Commands.
const (
	CmdStatus = "status" // current state
	CmdStart  = "start"  // import bases from the user's data dir and (re)start the engine with Strategy
	CmdStop   = "stop"   // stop the engine and remove pf rules
)

type Request struct {
	Cmd      string `json:"cmd"`
	Strategy string `json:"strategy,omitempty"` // strategy ID from strategies.json, for CmdStart
}

type Response struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Protocol int      `json:"protocol"`
	Version  string   `json:"version"` // daemon version
	Running  bool     `json:"running"`
	Strategy string   `json:"strategy,omitempty"`
	PID      int      `json:"pid,omitempty"`
	Iface    string   `json:"iface,omitempty"`
	LastExit string   `json:"last_exit,omitempty"` // why the engine stopped on its own, if it did
	Note     string   `json:"note,omitempty"`      // warning about the running engine (VPN active, …)
	Log      []string `json:"log,omitempty"`       // last engine output lines
}

// Call sends one request to the daemon and waits for the answer.
func Call(req Request, timeout time.Duration) (Response, error) {
	return call(SocketPath, req, timeout)
}

func call(socket string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("служба Zapretify не отвечает: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("ответ службы Zapretify: %w", err)
	}
	if !resp.OK && resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}
