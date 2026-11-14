// Command fakehost plays a 5250 host from a capture file so the TUI can be tried without an IBM i.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/DerKyberTakt/tn5250-tui/internal/fakehost"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:2323", "address to listen on")
	capPath := flag.String("capture", "testdata/pub400-signon-3477fc.json", "capture file to replay")
	strict := flag.Bool("strict", false, "close the connection when the client differs from the capture")
	flag.Parse()

	c, err := fakehost.LoadCapture(*capPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakehost:", err)
		os.Exit(1)
	}
	s, err := fakehost.StartWith(*listen, c, fakehost.Options{Lenient: !*strict})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakehost:", err)
		os.Exit(1)
	}
	fmt.Printf("fakehost: replaying %s on %s (try: tn5250-tui %s)\n", *capPath, s.Addr(), s.Addr())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	_ = s.Close()
}
