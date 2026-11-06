package ucwrap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// packageFixtures are the sample packages used by the wrap/obfuscate tests.
//
//   - UCW_*  are small hand-written packages covering a spread of PL/SQL
//     features (conditional compilation, local record/collection types,
//     cursors, labels/goto, dynamic SQL, quoted strings containing comment
//     markers). They compile cleanly against DUAL only.
//   - EBA_*  are packages taken from Oracle's APEX sample apps; they reference
//     tables absent from a bare schema, so DB tests compare compile errors
//     between plain and wrapped installs instead of requiring a clean compile.
//   - UCW_BIGGEN is generated in code (see genLargePackage). It spans hundreds
//     of base64 lines, the stress case for the wrapper's terminator length.
var committedFixtures = []string{
	"UCW_STRINGS",
	"UCW_COLLECTIONS",
	"UCW_CONDCOMP",
	"UCW_FLOW",
	"UCW_CURSORS",
	"EBA_CUST_FW",
	"EBA_DEMO_IR_PKG",
	"UCW_BIGGEN",
}

// packageFixtures is the committed list plus any private fixtures found in the
// git-ignored testdata/realworld/ directory (<OBJECT>.pkb, optional .pks).
// Those hold proprietary package sources that must never be committed; the
// tests simply skip them when the directory is absent (e.g. in CI).
var packageFixtures = func() []string {
	out := append([]string{}, committedFixtures...)
	files, _ := filepath.Glob(filepath.Join("..", "testdata", "realworld", "*.pkb"))
	sort.Strings(files)
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
	}
	return out
}()

// loadFixture returns the spec (optional) and body source for an object. Any
// trailing "/" terminator is stripped from the spec so callers can append
// their own. ok is false when the body is missing.
func loadFixture(t *testing.T, obj string) (specSQL, bodySrc string, ok bool) {
	t.Helper()
	if obj == "UCW_BIGGEN" {
		specSQL, bodySrc = genLargePackage(obj, 400)
		return specSQL, bodySrc, true
	}
	dir := filepath.Join("..", "testdata", "packages")
	b, err := os.ReadFile(filepath.Join(dir, obj+".pkb"))
	if err != nil {
		dir = filepath.Join("..", "testdata", "realworld")
		if b, err = os.ReadFile(filepath.Join(dir, obj+".pkb")); err != nil {
			return "", "", false
		}
	}
	bodySrc = string(b)
	if s, err := os.ReadFile(filepath.Join(dir, obj+".pks")); err == nil {
		spec := strings.TrimRight(string(s), " \t\r\n")
		spec = strings.TrimRight(spec, "/")
		specSQL = strings.TrimRight(spec, " \t\r\n")
	}
	return specSQL, bodySrc, true
}

// genLargePackage builds a spec and body with n small functions, each with a
// local variable and constant, so the wrapped unit spans many base64 lines.
func genLargePackage(name string, n int) (spec, body string) {
	var sp, bd strings.Builder
	fmt.Fprintf(&sp, "create or replace package %s as\n", strings.ToLower(name))
	fmt.Fprintf(&bd, "create or replace package body %s as\n", strings.ToLower(name))
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sp, "  function calc_%d (p_in in number) return number;\n", i)
		fmt.Fprintf(&bd, "  function calc_%d (p_in in number) return number\n  is\n", i)
		fmt.Fprintf(&bd, "    c_factor_%d constant number := %d;\n    l_work_%d    number;\n  begin\n", i, i, i)
		fmt.Fprintf(&bd, "    l_work_%d := p_in * c_factor_%d + %d;\n", i, i, i*7)
		fmt.Fprintf(&bd, "    if l_work_%d > 1000 then\n      l_work_%d := mod(l_work_%d, 1000);\n    end if;\n", i, i, i)
		fmt.Fprintf(&bd, "    return l_work_%d;\n  end calc_%d;\n\n", i, i)
	}
	fmt.Fprintf(&sp, "end %s;", strings.ToLower(name))
	fmt.Fprintf(&bd, "end %s;\n", strings.ToLower(name))
	return sp.String(), bd.String()
}
