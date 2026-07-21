package daemon

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// sdNotify sends a status datagram to systemd (the unit is Type=notify, so it
// waits for READY=1 before considering the service started). No-op when
// NOTIFY_SOCKET is unset, i.e. when running outside systemd.
//
// Written by hand rather than pulling a dependency: the protocol is literally
// one datagram of key=value text to the socket named in the environment
func sdNotify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}

	// A leading '@' means a linux abstract socket, whos real name starts with NUL.
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}

	// dial ipc connection, if fails return error
	conn, err := net.Dial("unixgram", addr)
	if err != nil {
		return fmt.Errorf("dial notify socket: %w", err)
	}
	defer conn.Close()

	// wirte state to the connection, throw error if fails to write state
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("write notify state: %w", err)
	}

	return nil
}
