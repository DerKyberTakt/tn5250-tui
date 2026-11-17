package runscript

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
)

func TestConnLost(t *testing.T) {
	for _, e := range []error{io.EOF, fmt.Errorf("database server: %w", syscall.ECONNRESET), errors.New("write tcp: wsasend: An existing connection was forcibly closed by the remote host."), errors.New("the connection is closed")} {
		if !connLost(e) {
			t.Errorf("%v: want lost", e)
		}
	}
	for _, e := range []error{nil, errors.New("sign-on refused: the password is not correct"), errors.New("SQL0204: not found")} {
		if connLost(e) {
			t.Errorf("%v: want kept", e)
		}
	}
}
