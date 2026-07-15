//go:build dbtest

// Real-world DB integration tests. Run with:
//
//	go test -tags dbtest -run RealWorld ./plsqlwrap
//
// These exercise wrap + obfuscate against a sample of genuine UC PL/SQL package
// bodies (including the explicitly requested guided-tour package) and install
// each into a live Oracle instance via SQLcl. They share the runSQL/sqlConn
// helpers with db_integration_test.go.
//
// The spec/body source files are committed under testdata/realworld/ as
// <OBJECT>.pks / <OBJECT>.pkb, so the suite is self-contained (no dependency on
// an external checkout).
package plsqlwrap

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// realWorldFixtures are the sampled package bodies, committed under
// testdata/realworld/. UC_GUIDEDTOUR was requested explicitly; the rest are a
// spread of sizes (≈370 → ≈6800 lines) picked across different products to
// stress the wrapper — in particular the terminator length field, which only
// misbehaves on units spanning many base64 lines.
var realWorldFixtures = []string{
	"UC_GUIDEDTOUR",
	"UC_IMAGEEDIT",
	"AOE_PLUGIN",
	"APE_UTILS",
	"AIW_CLAIM_API",
	// Larger / trickier units added to stress the wrapper and the obfuscator's
	// block-structure scanner:
	//   APE_DICTIONARY_VIEWS — ~11k lines / ~1500 base64 lines, the biggest unit.
	//   AOP_API25_PKG        — heavy PL/SQL conditional compilation ($IF/$THEN/$END).
	//   UC_GRID              — local RECORD/collection types + conditional compilation.
	//   UC_CRYPTO            — many locally-declared types (RECORD/TABLE OF).
	//   APE_JSON_DOC         — conditional compilation + BULK COLLECT.
	//   UC_APEX_CHAT         — mixed: types, pragmas, bulk, conditional compilation.
	"APE_DICTIONARY_VIEWS",
	"AOP_API25_PKG",
	"UC_GRID",
	"UC_CRYPTO",
	"APE_JSON_DOC",
	"UC_APEX_CHAT",
}

// loadFixture reads the spec (optional) and body source for an object from
// testdata/realworld/. ok is false when the .pkb is missing. Any trailing "/"
// terminator is stripped from the spec so the caller can append its own.
func loadFixture(t *testing.T, obj string) (specSQL, bodySrc string, ok bool) {
	t.Helper()
	dir := filepath.Join("..", "testdata", "realworld")
	b, err := os.ReadFile(filepath.Join(dir, obj+".pkb"))
	if err != nil {
		return "", "", false
	}
	bodySrc = string(b)
	if s, err := os.ReadFile(filepath.Join(dir, obj+".pks")); err == nil {
		spec := strings.TrimRight(string(s), " \t\r\n")
		spec = strings.TrimRight(spec, "/")
		specSQL = strings.TrimRight(spec, " \t\r\n")
	}
	return specSQL, bodySrc, true
}

// installResult captures what an install produced for one package body.
type installResult struct {
	errs      []string // sorted "line@@pos@@text" from USER_ERRORS for the body
	malformed int      // count of errors mentioning malformed/corrupted/PLS-00753
	exists    bool     // does the PACKAGE BODY object exist afterwards?
}

// installPackage installs the (optional) spec then the given unit (plain or
// wrapped), and reports the body's compile errors, malformed-unit count, and
// existence. It is dependency-robust: errors from missing tables/packages are
// returned verbatim so callers can compare two installs of the same logical
// source rather than requiring a clean compile.
func installPackage(t *testing.T, obj, specSQL, unitSQL string) installResult {
	t.Helper()
	up := strings.ToUpper(obj)
	var sb strings.Builder
	sb.WriteString("set feedback off\nset define off\nset heading off\nset serveroutput off\nset pagesize 0\nset linesize 32767\nset trimspool on\n")
	if specSQL != "" {
		sb.WriteString(specSQL)
		sb.WriteString("\n/\n")
	}
	sb.WriteString(unitSQL)
	sb.WriteString("\n")
	sb.WriteString("prompt <<<ERRBEGIN>>>\n")
	sb.WriteString("select line||'@@'||position||'@@'||text from user_errors where name='" + up + "' and type='PACKAGE BODY' order by line,position,text;\n")
	sb.WriteString("prompt <<<ERREND>>>\n")
	sb.WriteString("prompt <<<MAL>>>\n")
	sb.WriteString("select count(*) from user_errors where name='" + up + "' and type='PACKAGE BODY' and (upper(text) like '%MALFORMED%' or upper(text) like '%CORRUPT%' or text like '%PLS-00753%');\n")
	sb.WriteString("prompt <<<MALEND>>>\n")
	sb.WriteString("prompt <<<EX>>>\n")
	sb.WriteString("select count(*) from user_objects where object_name='" + up + "' and object_type='PACKAGE BODY';\n")
	sb.WriteString("prompt <<<EXEND>>>\n")

	out := runSQL(t, sb.String())

	var res installResult
	for _, line := range strings.Split(extractBetween(out, "<<<ERRBEGIN>>>", "<<<ERREND>>>"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			res.errs = append(res.errs, l)
		}
	}
	sort.Strings(res.errs)
	res.malformed = atoiSafe(extractBetween(out, "<<<MAL>>>", "<<<MALEND>>>"))
	res.exists = atoiSafe(extractBetween(out, "<<<EX>>>", "<<<EXEND>>>")) > 0
	return res
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// errTexts strips the leading "line@@pos@@" from each error so two installs can
// be compared by message alone. Obfuscation strips source formatting, so its
// error line numbers differ from the plain source's even when the underlying
// (dependency) errors are identical; comparing message text is line-agnostic.
func errTexts(errs []string) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		if p := strings.SplitN(e, "@@", 3); len(p) == 3 {
			out = append(out, p[2])
		} else {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// TestRealWorldWrapMatchesPlain proves Go-wrapped real package bodies install
// into Oracle and behave identically to their plain (un-wrapped) normalized
// source: Oracle never rejects the wrapped unit as malformed/corrupted, and the
// set of compile errors is byte-identical between the plain and wrapped install.
// Identical errors ⇒ wrapping is transparent to the compiler, independent of
// whatever external dependencies these packages happen to be missing in the
// test schema.
func TestRealWorldWrapMatchesPlain(t *testing.T) {
	for _, obj := range realWorldFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			specSQL, bodySrc, ok := loadFixture(t, obj)
			if !ok {
				t.Skipf("fixture not present: testdata/realworld/%s.pkb", obj)
			}
			t.Cleanup(func() { runSQL(t, "DROP PACKAGE "+obj+";\n") })

			wrapped := WrapV2(bodySrc, false, false)
			normalized, err := UnwrapV2(wrapped)
			if err != nil {
				t.Fatalf("UnwrapV2(WrapV2(body)): %v", err)
			}

			plain := installPackage(t, obj, specSQL, normalized)
			wrap := installPackage(t, obj, specSQL, wrapped)

			if wrap.malformed != 0 {
				t.Errorf("Oracle rejected the wrapped body as malformed/corrupted (%d such errors)", wrap.malformed)
			}
			if !wrap.exists {
				t.Errorf("wrapped PACKAGE BODY %s was not created", obj)
			}
			if !equalStrings(plain.errs, wrap.errs) {
				t.Errorf("compile errors differ between plain and wrapped install (wrap is not transparent)\nplain (%d):\n  %s\nwrapped (%d):\n  %s",
					len(plain.errs), strings.Join(plain.errs, "\n  "),
					len(wrap.errs), strings.Join(wrap.errs, "\n  "))
			} else {
				t.Logf("%s: %d base64 lines, %d compile error(s) identical plain vs wrapped, no malformed unit",
					obj, countB64Lines(wrapped), len(wrap.errs))
			}
		})
	}
}

// TestRealWorldObfuscateInstalls proves the obfuscate→wrap→install pipeline
// compiles equivalently to the original: Oracle accepts the wrapped unit (no
// malformed/corrupted error, object created) AND the obfuscated body produces
// the SAME set of compile-error messages as the plain normalized source. Since
// the obfuscator now only renames provably-local identifiers, it introduces no
// new errors — the only errors are the same missing-dependency ones the plain
// body has in this schema (often none). It also checks the pipeline is
// reversible: unwrap+deobfuscate restores the original identifier names.
func TestRealWorldObfuscateInstalls(t *testing.T) {
	const pw = "real-world-pass-123"
	for _, obj := range realWorldFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			specSQL, bodySrc, ok := loadFixture(t, obj)
			if !ok {
				t.Skipf("fixture not present: testdata/realworld/%s.pkb", obj)
			}
			t.Cleanup(func() { runSQL(t, "DROP PACKAGE "+obj+";\n") })

			// Baseline: the plain normalized source (errors here are pure
			// missing-dependency noise, identical for the obfuscated body).
			baseWrapped := WrapV2(bodySrc, false, false)
			normalized, err := UnwrapV2(baseWrapped)
			if err != nil {
				t.Fatalf("UnwrapV2(WrapV2(body)): %v", err)
			}
			base := installPackage(t, obj, specSQL, normalized)

			obf, err := Obfuscate(bodySrc, pw, false)
			if err != nil {
				t.Fatalf("Obfuscate: %v", err)
			}
			wrapped := WrapObfuscated(obf, false)

			res := installPackage(t, obj, specSQL, wrapped)
			if res.malformed != 0 {
				t.Errorf("Oracle rejected the obfuscated wrapped body as malformed/corrupted (%d such errors)", res.malformed)
			}
			if !res.exists {
				t.Errorf("obfuscated PACKAGE BODY %s was not created", obj)
			}
			if got, want := errTexts(res.errs), errTexts(base.errs); !equalStrings(got, want) {
				t.Errorf("obfuscation introduced compile errors (it should be semantically transparent)\nplain (%d):\n  %s\nobfuscated (%d):\n  %s",
					len(want), strings.Join(want, "\n  "),
					len(got), strings.Join(got, "\n  "))
			}

			// Reversibility: unwrap the wrapped unit, then deobfuscate with the
			// embedded encrypted map, and confirm the renamed local identifiers
			// are restored to their originals.
			parts := SplitEnc(wrapped)
			restored, err := Deobfuscate(UnwrapAll(parts.Body)+"\n"+parts.Enc, pw)
			if err != nil {
				t.Fatalf("Deobfuscate: %v", err)
			}
			restoredUp := strings.ToUpper(restored)
			locals := collectRenamableLocals(stripComments(bodySrc))
			missing := 0
			for up := range locals {
				if !strings.Contains(restoredUp, up) {
					if missing < 5 {
						t.Errorf("deobfuscation did not restore renamed local %q", up)
					}
					missing++
				}
			}
			t.Logf("%s: obfuscated+installed clean (errors == plain), %d local names renamed & restored (%d missing)",
				obj, len(locals), missing)
		})
	}
}

func countB64Lines(wrapped string) int {
	n := 0
	for _, l := range strings.Split(wrapped, "\n") {
		l = strings.TrimSpace(l)
		if len(l) >= 60 && !strings.ContainsAny(l, " ") {
			n++
		}
	}
	return n
}
