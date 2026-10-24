// Command prove runs checks, signs their results, and verifies them.
package main

import (
	"io"
	"os"
)

// Env is what Main needs from the process.
type Env struct {
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

func main() {
	dir, _ := os.Getwd()
	os.Exit(Main(os.Args[1:], Env{Dir: dir, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}))
}

// Main runs prove with args and returns the exit code.
func Main(args []string, env Env) int { return 2 }
