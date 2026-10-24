package main

import (
	"reflect"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
)

func TestZeroConfig(t *testing.T) {
	t.Run("AC-807-12 with no .prove.yaml, run --name t -- true then verify passes", func(t *testing.T) {
		repo, _ := setup(t, "")
		head := testutil.Head(t, repo)
		if code, out, errOut := prove(t, "verify"); code == 0 {
			t.Fatalf("verify with nothing recorded should fail\n%s%s", out, errOut)
		}
		if code, out, errOut := prove(t, "run", "--name", "t", "--", "true"); code != 0 {
			t.Fatalf("prove run exited %d\n%s%s", code, out, errOut)
		}
		recs := records(t, repo, head)
		if len(recs) != 1 {
			t.Fatalf("want 1 record, got %d", len(recs))
		}
		if got := recs[0].Predicate.Inputs; !reflect.DeepEqual(got, []string{"**"}) {
			t.Fatalf("inputs = %v, want the whole repo", got)
		}
		if code, out, errOut := prove(t, "verify"); code != 0 {
			t.Fatalf("prove verify exited %d\n%s%s", code, out, errOut)
		}

		// A new commit changes the whole-repo inputs, so the record no
		// longer covers it.
		testutil.WriteFile(t, repo, "b.txt", "b\n")
		testutil.Commit(t, repo, "b")
		if code, out, errOut := prove(t, "verify"); code == 0 {
			t.Fatalf("verify of a new commit with nothing recorded should fail\n%s%s", out, errOut)
		}
	})
}
