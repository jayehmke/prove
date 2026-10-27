package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
)

// TestMain runs this test binary as prove when it is started under that
// name, so tests can run prove from a shell without building it.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "prove" {
		os.Exit(Main(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// quickstart returns the commands of README.md's ```sh quickstart block.
func quickstart(t *testing.T) []string {
	t.Helper()
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(readme), "\n```sh quickstart\n")
	if !ok {
		t.Fatal("README.md has no ```sh quickstart block")
	}
	block, _, ok = strings.Cut(block, "\n```")
	if !ok {
		t.Fatal("README.md's quickstart block is not closed")
	}
	var cmds []string
	for _, line := range strings.Split(block, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			cmds = append(cmds, line)
		}
	}
	return cmds
}

func TestReadmeQuickstartRuns(t *testing.T) {
	t.Run("AC-808-4 the README quickstart runs in order in a fresh git repo with no config and ends with a passing verify", func(t *testing.T) {
		// Keep the usual Go build cache: the quickstart runs go test.
		gocache := strings.TrimSpace(goEnv(t, "GOCACHE"))
		gomodcache := strings.TrimSpace(goEnv(t, "GOMODCACHE"))

		cmds := quickstart(t)
		if len(cmds) == 0 || !strings.HasPrefix(cmds[len(cmds)-1], "prove verify") {
			t.Fatalf("the quickstart should end with prove verify: %q", cmds)
		}

		// "your-repo": a small Go module with a passing test, committed,
		// and no .prove.yaml.
		repo := testutil.NewRepo(t)
		testutil.WriteFile(t, repo, "go.mod", "module example.com/hello\n\ngo 1.22\n")
		testutil.WriteFile(t, repo, "hello.go", "package hello\n\nfunc Hello() string { return \"hello\" }\n")
		testutil.WriteFile(t, repo, "hello_test.go", "package hello\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(Hello())\n\t}\n}\n")
		testutil.Commit(t, repo, "hello")
		// The developer's own SSH key, where prove looks by default.
		key := testutil.NewKey(t, "dev@example.com")
		sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(key.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), data, 0o600); err != nil {
			t.Fatal(err)
		}

		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		if err := os.Symlink(self, filepath.Join(bin, "prove")); err != nil {
			t.Fatal(err)
		}

		env := append(os.Environ(),
			"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GOCACHE="+gocache, "GOMODCACHE="+gomodcache, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
		var log strings.Builder
		for _, c := range cmds {
			cmd := exec.Command("sh", "-c", c)
			cmd.Dir = repo.Dir
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			log.WriteString("$ " + c + "\n" + string(out))
			if err != nil {
				t.Fatalf("quickstart command %q failed: %v\n%s", c, err, log.String())
			}
		}
		if !strings.Contains(log.String(), "\nPASS\n") {
			t.Fatalf("the quickstart's verify did not pass:\n%s", log.String())
		}
		t.Logf("\n%s", log.String())
	})
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return string(out)
}
