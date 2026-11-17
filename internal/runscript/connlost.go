package runscript

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// connLost tells a host-server connection that is gone (the server job ended, the
// network dropped, a firewall closed an idle socket) from an ordinary failure such as a
// refused sign-on or an SQL error.
func connLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	m := strings.ToLower(err.Error())
	for _, s := range []string{"connection is closed", "forcibly closed", "connection reset", "broken pipe", "use of closed", "connection abort", "wsarecv", "wsasend"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}
