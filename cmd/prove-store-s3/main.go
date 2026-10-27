// Command prove-store-s3 is the prove store adapter for S3. See
// docs/adapters.md.
package main

import (
	"context"
	"os"

	"github.com/jayehmke/prove/adapters/s3"
)

func main() { os.Exit(s3.Main(context.Background(), os.Stdin, os.Stdout, os.Stderr)) }
