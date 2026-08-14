package ruleset_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestStrictBaseSuite runs the strict base ruleset's own .tln.test on every
// build, so the shipped safety policy stays verified. The tln CLI is the
// canonical .tln.test runner (internal/testrunner is not importable); it is
// pinned as a `tool` dependency in go.mod and invoked via `go tool` — the same
// runner a tenant gets when they copy talooner.tln + talooner.tln.test into
// their repo.
func TestStrictBaseSuite(t *testing.T) {
	cmd := exec.Command("go", "tool",
		"github.com/opentalon/tln-language/cmd/tln", "test",
		"base/talooner.tln", "base/talooner.tln.test")
	out, err := cmd.CombinedOutput()
	t.Logf("tln test output:\n%s", out)
	if err != nil {
		t.Fatalf("tln test failed: %v", err)
	}
	if !strings.Contains(string(out), "0 failed") {
		t.Fatalf("strict base suite did not pass cleanly:\n%s", out)
	}
}
