// Command prove-store-github is the prove store adapter that keeps records
// as GitHub commit comments. See docs/adapters.md.
package main

import (
	"context"
	"os"

	"github.com/jayehmke/prove/adapters/github"
)

func main() { os.Exit(github.Main(context.Background(), os.Stdin, os.Stdout, os.Stderr)) }
