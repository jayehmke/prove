// Command prove runs checks, signs their results and verifies them.
package main

import (
	"io"
	"os"
)

func main() { os.Exit(Main(os.Args[1:], os.Stdout, os.Stderr)) }

// Main runs prove with args in the current directory and returns the exit
// code: 0 success, 1 a check or verification failed, 2 a usage or setup
// error.
func Main(args []string, stdout, stderr io.Writer) int {
	return 2
}
