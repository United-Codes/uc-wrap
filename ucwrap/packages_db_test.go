//go:build dbtest

// Package DB integration tests. Run with:
//
//	go test -tags dbtest -run Packages ./ucwrap
//
// These exercise wrap + obfuscate against the sample packages defined in
// packages_fixtures_test.go (committed under testdata/packages/ plus a
// generated large unit) and install each into a live Oracle instance via
// SQLcl. They share the runSQL/sqlConn helpers with db_integration_test.go.
package ucwrap

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

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

// TestPackagesWrapMatchesPlain proves Go-wrapped real package bodies install
// into Oracle and behave identically to their plain (un-wrapped) normalized
// source: Oracle never rejects the wrapped unit as malformed/corrupted, and the
// set of compile errors is byte-identical between the plain and wrapped install.
// Identical errors ⇒ wrapping is transparent to the compiler, independent of
// whatever external dependencies these packages happen to be missing in the
// test schema.
func TestPackagesWrapMatchesPlain(t *testing.T) {
	for _, obj := range packageFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			specSQL, bodySrc, ok := loadFixture(t, obj)
			if !ok {
				t.Skipf("fixture not present: testdata/packages/%s.pkb", obj)
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

// TestPackagesObfuscateInstalls proves the obfuscate→wrap→install pipeline
// compiles equivalently to the original: Oracle accepts the wrapped unit (no
// malformed/corrupted error, object created) AND the obfuscated body produces
// the SAME set of compile-error messages as the plain normalized source. Since
// the obfuscator now only renames provably-local identifiers, it introduces no
// new errors — the only errors are the same missing-dependency ones the plain
// body has in this schema (often none). It also checks the pipeline is
// reversible: unwrap+deobfuscate restores the original identifier names.
func TestPackagesObfuscateInstalls(t *testing.T) {
	const pw = "real-world-pass-123"
	for _, obj := range packageFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			specSQL, bodySrc, ok := loadFixture(t, obj)
			if !ok {
				t.Skipf("fixture not present: testdata/packages/%s.pkb", obj)
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
			locals := renamableLocals(stripComments(bodySrc))
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

const captureSpec = `create or replace package ucw_capture as
  function run return varchar2;
end ucw_capture;`

// captureBody exercises the situations where naively renaming a "local" changes
// behaviour without any compile error: a local named like a table column
// (status), a local named like a record field / parameter / named argument
// (amount), and a q'[...]' literal containing identifier names and quotes.
const captureBody = `create or replace package body ucw_capture as
  status   varchar2(10) := 'Z';
  amount   number := 99;
  g_note   varchar2(100) := q'[it's amount status -- literal]';
  g_total  number;
  type t_rec is record (amount number, label varchar2(10));

  function sum_big (amount in number) return number
  is
    l_rec t_rec;
    l_n   number;
  begin
    l_rec.amount := amount * 2;
    select count(*) into l_n from ucw_cap_t where status = 'A' and ucw_cap_t.amount > 0;
    return l_rec.amount + l_n;
  end sum_big;

  function run return varchar2
  is
  begin
    g_total := sum_big(amount => 5);
    return g_note || '|' || g_total || '|' || status;
  end run;
end ucw_capture;`

// runCapture installs the table + spec + the given body and returns what
// ucw_capture.run produces.
func runCapture(t *testing.T, unit string) string {
	t.Helper()
	script := "set feedback off\nset define off\nset heading off\nset pagesize 0\nset linesize 1000\n" +
		"begin execute immediate 'drop table ucw_cap_t purge'; exception when others then null; end;\n/\n" +
		"create table ucw_cap_t (status varchar2(10), amount number);\n" +
		"insert into ucw_cap_t values ('A', 1);\ninsert into ucw_cap_t values ('A', 2);\ninsert into ucw_cap_t values ('B', 3);\ncommit;\n" +
		captureSpec + "\n/\n" + unit + "\n" +
		"prompt <<<B>>>\nselect ucw_capture.run from dual;\nprompt <<<E>>>\n" +
		"drop package ucw_capture;\ndrop table ucw_cap_t purge;\n"
	return strings.TrimSpace(extractBetween(runSQL(t, script), "<<<B>>>", "<<<E>>>"))
}

// TestPackagesObfuscatedBehaviourMatchesPlain installs a plain and an
// obfuscated+wrapped version of the same package and requires identical
// runtime results (not merely identical compile errors).
func TestPackagesObfuscatedBehaviourMatchesPlain(t *testing.T) {
	plain := runCapture(t, captureBody+"\n/")
	const want = "it's amount status -- literal|12|Z"
	if plain != want {
		t.Fatalf("plain run = %q, want %q (test setup problem)", plain, want)
	}
	obf, err := Obfuscate(captureBody, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(SplitEnc(obf).Body, "g_total") {
		t.Errorf("expected g_total to be renamed:\n%s", SplitEnc(obf).Body)
	}
	if got := runCapture(t, WrapObfuscated(obf, false)); got != plain {
		t.Errorf("obfuscated behaviour differs from plain:\n got  %q\n want %q", got, plain)
	}
}

// TestPackagesObfuscateSpecAndBodyInOneFile verifies that a spec and body in a
// single file can be obfuscated together (layout is preserved, so
// WrapObfuscated can find each CREATE) and that the result installs and runs.
func TestPackagesObfuscateSpecAndBodyInOneFile(t *testing.T) {
	spec, body, _ := loadFixture(t, "UCW_STRINGS")
	obf, err := Obfuscate(spec+"\n/\n"+body, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := WrapObfuscated(obf, false)
	if n := strings.Count(wrapped, " wrapped \n"); n != 2 {
		t.Fatalf("expected 2 wrapped units, got %d", n)
	}
	t.Cleanup(func() { runSQL(t, "DROP PACKAGE ucw_strings;\n") })
	script := "set feedback off\nset define off\nset heading off\nset pagesize 0\n" +
		wrapped + "\nprompt <<<B>>>\nselect ucw_strings.reverse_text('abc') || ucw_strings.count_words('a b c') from dual;\nprompt <<<E>>>\n"
	if got := strings.TrimSpace(extractBetween(runSQL(t, script), "<<<B>>>", "<<<E>>>")); got != "cba3" {
		t.Errorf("got %q, want cba3", got)
	}
}
