package ucwrap

import (
	"strings"
	"testing"
)

const obfProc = "CREATE OR REPLACE PROCEDURE test_obf IS\n" +
	"  v_counter NUMBER := 0;\n" +
	"  v_total   NUMBER := 0;\n" +
	"BEGIN\n" +
	"  v_counter := v_counter + 1;\n" +
	"  v_total := v_total + v_counter;\n" +
	"END;\n"

func TestObfuscateEmitsEncBlock(t *testing.T) {
	out, err := Obfuscate(obfProc, "secret", false)
	if err != nil {
		t.Fatalf("Obfuscate error: %v", err)
	}
	if !strings.Contains(out, "\n--ENC:\n") {
		t.Error("missing --ENC: marker")
	}
	if !strings.Contains(out, "--ENCEND") {
		t.Error("missing --ENCEND marker")
	}
}

func TestObfuscateRemovesLocalNames(t *testing.T) {
	out, err := Obfuscate(obfProc, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	body := SplitEnc(out).Body
	if strings.Contains(body, "v_counter") || strings.Contains(body, "v_total") {
		t.Errorf("local identifiers should be renamed in body:\n%s", body)
	}
	// The object name must be preserved (protected).
	if !strings.Contains(body, "test_obf") {
		t.Errorf("object name should be preserved:\n%s", body)
	}
}

func TestObfuscateDeobfuscateRoundTrip(t *testing.T) {
	out, err := Obfuscate(obfProc, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Deobfuscate(out, "secret")
	if err != nil {
		t.Fatalf("Deobfuscate error: %v", err)
	}
	for _, name := range []string{"v_counter", "v_total"} {
		if !strings.Contains(restored, name) {
			t.Errorf("deobfuscation did not restore %q:\n%s", name, restored)
		}
	}
}

func TestDeobfuscateWrongPassphrase(t *testing.T) {
	out, err := Obfuscate(obfProc, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Deobfuscate(out, "wrong"); err == nil {
		t.Error("expected error with wrong passphrase")
	}
}

func TestDeobfuscateNoEncData(t *testing.T) {
	if _, err := Deobfuscate("CREATE OR REPLACE FUNCTION f IS BEGIN NULL; END;", "x"); err == nil {
		t.Error("expected ErrNoEncData when no --ENC: block present")
	}
}

func TestObfuscateProtectsParameters(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION calc(p_input NUMBER, p_factor NUMBER) RETURN NUMBER IS\n" +
		"  v_intermediate NUMBER;\n" +
		"BEGIN\n" +
		"  v_intermediate := p_input * p_factor;\n" +
		"  RETURN v_intermediate;\n" +
		"END;\n"
	out, err := Obfuscate(src, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	body := SplitEnc(out).Body
	for _, p := range []string{"p_input", "p_factor"} {
		if !strings.Contains(body, p) {
			t.Errorf("parameter %q should be protected from renaming:\n%s", p, body)
		}
	}
	if strings.Contains(body, "v_intermediate") {
		t.Errorf("local variable should be renamed:\n%s", body)
	}
}

func TestMakeMappingSameLengthAndUnique(t *testing.T) {
	names := []string{"L_COUNTER", "L_TOTAL", "G_CACHE", "ABC"}
	allIDs := map[string]struct{}{"BEGIN": {}}
	m := makeMapping(names, allIDs, map[string]struct{}{})
	seen := map[string]bool{}
	for _, n := range names {
		g, ok := m[n]
		if !ok {
			t.Fatalf("no mapping for %s", n)
		}
		if len(g) != len(n) {
			t.Errorf("%s -> %q: length %d != %d", n, g, len(g), len(n))
		}
		if !isIDStart(g[0]) {
			t.Errorf("%q does not start with a letter", g)
		}
		if strings.ToUpper(g) == n || seen[strings.ToUpper(g)] {
			t.Errorf("%q is not unique/new", g)
		}
		if !strings.ContainsAny(g[1:], "0123456789_") {
			t.Errorf("%q has no digit/underscore", g)
		}
		seen[strings.ToUpper(g)] = true
	}
}

func TestObfuscatePreservesLayout(t *testing.T) {
	src := "CREATE OR REPLACE PROCEDURE test_obf IS\n" +
		"  /* block\n     comment */\n" +
		"  v_counter NUMBER := 0; -- trailing\n" +
		"BEGIN\n" +
		"  v_counter := v_counter + 1;\n" +
		"END;\n"
	out, err := Obfuscate(src, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(SplitEnc(out).Body, "\n"), "\n")
	want := strings.Split(strings.TrimRight(stripComments(src), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("line count %d, want %d\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if len(strings.TrimRight(got[i], " ")) != len(strings.TrimRight(want[i], " ")) {
			t.Errorf("line %d length changed:\n got  %q\n want %q", i+1, got[i], want[i])
		}
	}
	// And it restores exactly (comments excepted).
	restored, err := Deobfuscate(out, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(restored, "\n") != strings.TrimRight(stripComments(src), "\n") {
		t.Errorf("deobfuscated text differs from comment-stripped source:\n%s", restored)
	}
}

// The safety rules: a name is only renamed when it can mean nothing else.
func TestObfuscateSafety(t *testing.T) {
	src := `CREATE OR REPLACE PACKAGE BODY safety_pkg AS
  g_ok      NUMBER := 1;
  g_cache   NUMBER;
  status    VARCHAR2(10);
  amount    NUMBER;
  g_after   NUMBER;
  g_note    VARCHAR2(100) := q'[it's a g_ok -- literal]';
  TYPE t_rec IS RECORD (amount NUMBER, g_cache NUMBER);

  PROCEDURE do_it(p_x IN NUMBER, amount IN NUMBER) IS
    l_rec t_rec;
    l_out NUMBER;
  BEGIN
    SELECT 1 INTO l_out FROM dual WHERE 1 = 1 AND status = 'A';
    l_rec.amount := amount;
    g_ok := g_ok + safety_pkg.g_cache;
    do_it(p_x => 1, amount => 2);
    g_after := l_out;
  END do_it;
END safety_pkg;
`
	locals := renamableLocals(src)
	for _, n := range []string{"G_OK", "G_AFTER", "L_OUT", "L_REC", "T_REC", "G_NOTE"} {
		if _, ok := locals[n]; !ok {
			t.Errorf("%s should be renamable", n)
		}
	}
	for n, why := range map[string]string{
		"STATUS":  "used in static SQL (could be a column)",
		"AMOUNT":  "record field / parameter / named argument",
		"G_CACHE": "qualified reference and record field",
	} {
		if _, ok := locals[n]; ok {
			t.Errorf("%s must NOT be renamable: %s", n, why)
		}
	}

	out, err := Obfuscate(src, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	body := SplitEnc(out).Body
	for _, keep := range []string{"status = 'A'", "amount => 2", "p_x => 1", "safety_pkg.g_cache", "RECORD (amount NUMBER, g_cache NUMBER)", "q'[it's a g_ok -- literal]'"} {
		if !strings.Contains(body, keep) {
			t.Errorf("expected %q to survive verbatim:\n%s", keep, body)
		}
	}
	if strings.Contains(body, "g_after") || strings.Contains(body, "l_out") {
		t.Errorf("safe locals should be renamed:\n%s", body)
	}
}

func TestSpecDeclarationsNeverRenamed(t *testing.T) {
	spec := "CREATE OR REPLACE PACKAGE api_pkg AS\n  gc_public CONSTANT NUMBER := 1;\n  TYPE t_pub IS TABLE OF NUMBER;\n  e_pub EXCEPTION;\nEND api_pkg;\n"
	if got := renamableLocals(spec); len(got) != 0 {
		t.Errorf("package spec declarations are public API; got renamable %v", got)
	}
}

func TestLexemeEnd(t *testing.T) {
	tests := []struct {
		in   string
		kind byte
		want string
	}{
		{"'it''s' x", 's', "'it''s'"},
		{"q'[a'b]' x", 's', "q'[a'b]'"},
		{"Q'{a'b}' x", 's', "Q'{a'b}'"},
		{"nq'!a'b!' x", 's', "nq'!a'b!'"},
		{`"MixedCase" x`, 'd', `"MixedCase"`},
		{"-- hi\nx", 'c', "-- hi"},
		{"/* a\nb */ x", 'c', "/* a\nb */"},
		{"n'abc' x", 0, ""},
		{"quote x", 0, ""},
	}
	for _, tt := range tests {
		end, kind := lexemeEnd(tt.in, 0)
		if kind != tt.kind || (kind != 0 && tt.in[:end] != tt.want) {
			t.Errorf("lexemeEnd(%q) = %q kind %q; want %q kind %q", tt.in, tt.in[:end], kind, tt.want, tt.kind)
		}
	}
}

func TestMappingStringRoundTrip(t *testing.T) {
	m := map[string]string{"a": "v_counter", "b": "v_total"}
	s := mappingToString(m)
	back := stringToMapping(s)
	if len(back) != len(m) {
		t.Fatalf("mapping size mismatch: got %d want %d", len(back), len(m))
	}
	for k, v := range m {
		if back[k] != v {
			t.Errorf("mapping[%q] = %q, want %q", k, back[k], v)
		}
	}
}
