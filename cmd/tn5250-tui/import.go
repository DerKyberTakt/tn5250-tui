package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/vscode"
)

// runImport is `tn5250-tui import`: the VS Code extension's (and the desktop app's)
// settings.json into the sites file. Passwords never come across (VS Code keeps them in
// its secret store); an existing site keeps its password.
func runImport(args []string) int {
	fs := flag.NewFlagSet("tn5250-tui import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	from := fs.String("from", "", "settings.json to read (default: the first of VS Code, Insiders, VSCodium, Cursor)")
	dry := fs.Bool("dry-run", false, "show what would change, write nothing")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui import [--from settings.json] [--dry-run]")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nlooks in:")
		for _, c := range vscode.Candidates() {
			fmt.Fprintln(os.Stderr, "  "+c)
		}
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	r, path, err := vscode.ImportFile(*from)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui import:", err)
		return 1
	}
	f, err := sites.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tn5250-tui import: sites file %s: %v\n", sites.Path(), err)
		return 1
	}
	added, updated := vscode.Apply(f, r)
	fmt.Println("from:", path)
	for _, s := range r.Sites {
		fmt.Printf("  site %-20s %s:%d", s.Name, s.Host, s.WithDefaults().Port)
		if s.User != "" {
			fmt.Printf("  user %s", s.User)
		}
		if s.Demo {
			fmt.Print("  demo")
		}
		fmt.Println()
	}
	for _, n := range r.Notes {
		fmt.Println("  skipped:", n)
	}
	fmt.Println(vscode.Summary(r, added, updated))
	if *dry {
		fmt.Println("dry run: nothing written")
		return 0
	}
	if err := f.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui import: save:", err)
		return 1
	}
	fmt.Println("written:", sites.Path())
	return 0
}
