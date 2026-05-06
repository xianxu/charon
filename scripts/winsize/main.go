// Diagnostic: report TIOCGWINSZ from every fd we can reach, plus env.
// Used to track down cmux PTY size discrepancies (charon issue under
// investigation): bubbletea queries os.Stdout via term.GetSize and gets
// a wrong/stuck value, while `stty size` (which reads /dev/tty) reports
// something else. This program prints all of them side by side so we
// can see which fd to actually trust.
//
// Usage: go run ./scripts/winsize
package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func report(name string, fd uintptr) {
	w, h, err := term.GetSize(int(fd))
	if err != nil {
		fmt.Printf("%-12s fd=%d  ERROR: %v\n", name, fd, err)
		return
	}
	fmt.Printf("%-12s fd=%d  %dx%d\n", name, fd, w, h)
}

func main() {
	report("stdin", os.Stdin.Fd())
	report("stdout", os.Stdout.Fd())
	report("stderr", os.Stderr.Fd())

	if tty, err := os.Open("/dev/tty"); err == nil {
		defer tty.Close()
		report("/dev/tty", tty.Fd())
	} else {
		fmt.Printf("/dev/tty    open error: %v\n", err)
	}

	fmt.Printf("env LINES=%q COLUMNS=%q TERM=%q TERM_PROGRAM=%q\n",
		os.Getenv("LINES"), os.Getenv("COLUMNS"),
		os.Getenv("TERM"), os.Getenv("TERM_PROGRAM"))
}
