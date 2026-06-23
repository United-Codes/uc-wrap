package plsqlwrap

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

func TestShortName(t *testing.T) {
	// Matches the reference generator: append 'a'+(n%26) least-significant first.
	tests := map[int]string{0: "a", 1: "b", 25: "z", 26: "ab", 27: "bb"}
	for idx, want := range tests {
		if got := shortName(idx); got != want {
			t.Errorf("shortName(%d) = %q, want %q", idx, got, want)
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
