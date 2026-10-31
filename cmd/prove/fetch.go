package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// cmdFetch copies the records another store keeps for some commits into
// this repository's store, where verify reads them. The other store is
// untrusted: a record whose bytes don't hash to its id is refused here,
// and verify checks every signature as it always does.
func cmdFetch(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	from := fs.String("from", "", "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil || *from == "" {
		return 0, usageError{"fetch is: prove fetch --from <store> [commit...]"}
	}
	if len(pos) == 0 {
		pos = []string{"HEAD"}
	}
	e, err := load(ctx)
	if err != nil {
		return 0, err
	}
	fetched, refused := 0, 0
	for _, rev := range pos {
		out, err := e.repo.Git(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if err != nil {
			return 0, fmt.Errorf("%q is not a commit", rev)
		}
		commit := strings.TrimSpace(string(out))
		// "commit" tells an adapter where to look first; one that ignores it still works.
		src, err := store.Open(ctx, config.StoreConfig{Type: *from, Options: map[string]any{"commit": commit}}, e.repo)
		if err != nil {
			return 0, err
		}
		if src.Name() == e.store.Name() {
			return 0, usageError{"prove fetch --from names this repository's own store"}
		}
		metas, err := src.List(ctx, store.Query{Commit: commit})
		if err != nil {
			return 0, err
		}
		for _, m := range metas {
			data, err := src.Get(ctx, m.ID)
			if err != nil {
				return 0, err
			}
			if record.ID(data) != m.ID {
				fmt.Fprintf(stderr, "prove: refusing record %s from the %s: its bytes hash to %s\n", short(m.ID), src.Name(), short(record.ID(data)))
				refused++
				continue
			}
			if err := e.store.Put(ctx, m, data); err != nil {
				return 0, err
			}
			fetched++
		}
	}
	fmt.Fprintf(stdout, "fetched %d record(s), refused %d\n", fetched, refused)
	return exitOK, nil
}
