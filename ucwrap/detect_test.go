package ucwrap

import (
	"strings"
	"testing"
)

func TestUnwrapAllMultiSection(t *testing.T) {
	// A leading plain line followed by two wrapped units. Each wrapped unit is
	// unwrapped independently; text before the first unit is preserved. (Text
	// between units is absorbed into the preceding unit, matching the
	// reference's unit-boundary behavior.)
	prefix := "-- header comment\n"
	w1 := WrapV2("CREATE OR REPLACE FUNCTION f1 RETURN NUMBER IS BEGIN RETURN 1; END;", false, false)
	w2 := WrapV2("CREATE OR REPLACE FUNCTION f2 RETURN NUMBER IS BEGIN RETURN 2; END;", false, false)
	combined := prefix + w1 + w2

	out := UnwrapAll(combined)
	if !strings.Contains(out, "FUNCTION f1") {
		t.Error("first unit not unwrapped")
	}
	if !strings.Contains(out, "FUNCTION f2") {
		t.Error("second unit not unwrapped")
	}
	if !strings.Contains(out, "-- header comment") {
		t.Error("plain text before the first unit not preserved")
	}
}

func TestUnwrapAllPassthrough(t *testing.T) {
	plain := "SELECT * FROM dual;\n"
	if out := UnwrapAll(plain); out != plain {
		t.Errorf("plain SQL should pass through unchanged, got %q", out)
	}
}

func TestApplyCompat(t *testing.T) {
	in := "CREATE OR REPLACE FUNCTION myschema.f RETURN NUMBER IS\nBEGIN\n RETURN 1;\nEND;\n"
	out := ApplyCompat(in)
	if strings.Contains(out, "myschema.f") {
		t.Errorf("schema prefix not stripped: %q", out)
	}
	if !strings.Contains(out, "FUNCTION f ") {
		t.Errorf("expected bare object name 'f', got: %q", out)
	}
}

func TestApplyCompatNoSchema(t *testing.T) {
	in := "CREATE OR REPLACE PROCEDURE p IS\nBEGIN\n NULL;\nEND;\n"
	if out := ApplyCompat(in); out != in {
		t.Errorf("input without schema prefix should be unchanged, got %q", out)
	}
}
