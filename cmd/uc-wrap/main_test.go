package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unit = "CREATE OR REPLACE PROCEDURE %s IS\nBEGIN\n  NULL;\nEND;\n/\n"

func writeUnits(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		body := strings.Replace(unit, "%s", strings.TrimSuffix(strings.TrimSuffix(n, ".pkb"), ".sql"), 1)
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestExpandInputsGlob(t *testing.T) {
	dir := writeUnits(t, "b.pkb", "a.pkb", "c.sql")
	got, err := expandInputs([]string{filepath.Join(dir, "*.pkb")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0]) != "a.pkb" || filepath.Base(got[1]) != "b.pkb" {
		t.Errorf("got %v, want sorted a.pkb b.pkb", got)
	}
	// Shell-expanded list passes through, duplicates collapse.
	got, _ = expandInputs([]string{got[0], got[1], got[0]})
	if len(got) != 2 {
		t.Errorf("duplicates not collapsed: %v", got)
	}
	if _, err := expandInputs([]string{filepath.Join(dir, "*.nomatch")}); err == nil {
		t.Error("expected error for a pattern that matches nothing")
	}
}

func TestRunBatchWritesPkwNextToEachInput(t *testing.T) {
	dir := writeUnits(t, "one.pkb", "two.pkb")
	in := []string{filepath.Join(dir, "one.pkb"), filepath.Join(dir, "two.pkb")}
	if code := runBatch(options{cmd: "wrap"}, in, ""); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, n := range []string{"one.pkw", "two.pkw"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("%s not written: %v", n, err)
		}
		if !strings.Contains(string(b), " wrapped \n") {
			t.Errorf("%s is not a wrapped unit", n)
		}
	}
}

func TestRunBatchRejections(t *testing.T) {
	dir := writeUnits(t, "x.pkb", "y.sql")
	x, y := filepath.Join(dir, "x.pkb"), filepath.Join(dir, "y.sql")
	if code := runBatch(options{cmd: "wrap"}, []string{x, x + "2"}, "out.pkw"); code == 0 {
		t.Error("-o with several inputs must fail")
	}
	if code := runBatch(options{cmd: "deobfuscate", passphrase: "p"}, []string{x, y}, ""); code == 0 {
		t.Error("deobfuscate with several inputs must fail")
	}
	// A non-.pkb input fails but the .pkb is still processed.
	if code := runBatch(options{cmd: "wrap"}, []string{x, y}, ""); code == 0 {
		t.Error("non-.pkb input must be reported as a failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.pkw")); err != nil {
		t.Error("x.pkw should still have been written")
	}
}

func TestObfuscateBatch(t *testing.T) {
	dir := writeUnits(t, "p.pkb", "q.pkb")
	in := []string{filepath.Join(dir, "p.pkb"), filepath.Join(dir, "q.pkb")}
	if code := runBatch(options{cmd: "obfuscate", passphrase: "pw"}, in, ""); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "p.pkw"))
	if !strings.Contains(string(b), "--ENC:") {
		t.Error("obfuscated output should carry the --ENC: block")
	}
	if code := runBatch(options{cmd: "obfuscate"}, in, ""); code == 0 {
		t.Error("obfuscate without passphrase must fail")
	}
}
