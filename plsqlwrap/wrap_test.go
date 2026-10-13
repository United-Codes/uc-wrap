package plsqlwrap

import (
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single-line", "a := 1; -- comment\nb := 2;", "a := 1; \nb := 2;"},
		{"multi-line", "a := 1; /* c\nomment */ b := 2;", "a := 1;  b := 2;"},
		{"comment markers in string", "x := '-- not a comment';", "x := '-- not a comment';"},
		{"slashstar in string", "x := '/* not */';", "x := '/* not */';"},
		{"escaped quote", "x := 'it''s ok'; -- gone", "x := 'it''s ok'; "},
		{"no comments", "x := 1;", "x := 1;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripComments(tt.in); got != tt.want {
				t.Errorf("stripComments(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractHeader(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"CREATE OR REPLACE FUNCTION f RETURN NUMBER IS BEGIN END;", "CREATE OR REPLACE FUNCTION f"},
		{"CREATE OR REPLACE PROCEDURE p(a NUMBER) IS BEGIN END;", "CREATE OR REPLACE PROCEDURE p"},
		{"CREATE OR REPLACE PACKAGE BODY pkg AS BEGIN END;", "CREATE OR REPLACE PACKAGE BODY pkg"},
		{"CREATE OR REPLACE FUNCTION f\n RETURN NUMBER IS", "CREATE OR REPLACE FUNCTION f"},
		{"CREATE OR REPLACE PROCEDURE \"MyProc\" IS BEGIN END;", "CREATE OR REPLACE PROCEDURE \"MyProc\""},
	}
	for _, tt := range tests {
		if got := extractHeader(tt.in); got != tt.want {
			t.Errorf("extractHeader(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUpperPreservingStrings(t *testing.T) {
	in := "select 'Keep This' from dual"
	want := "SELECT 'Keep This' FROM DUAL"
	if got := upperPreservingStrings(in); got != want {
		t.Errorf("upperPreservingStrings(%q) = %q, want %q", in, got, want)
	}
}

func TestWrapV2OutputShape(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;"
	out := WrapV2(src, false, false)

	if !strings.HasPrefix(out, "CREATE OR REPLACE FUNCTION f wrapped \n") {
		t.Errorf("missing/incorrect header line, got start: %q", firstLine(out))
	}
	if !strings.Contains(out, "\na000000\n") {
		t.Error("missing a000000 marker")
	}
	if n := strings.Count(out, "abcd\n"); n != 15 {
		t.Errorf("expected 15 abcd preamble lines, got %d", n)
	}
	if !strings.HasSuffix(out, "\n/\n") {
		t.Error("expected trailing newline + slash")
	}
	// A terminator line "<hex> <hex>" must be present.
	foundTerm := false
	for _, line := range strings.Split(out, "\n") {
		if isHexLine(line) {
			foundTerm = true
			break
		}
	}
	if !foundTerm {
		t.Error("no hex terminator line found")
	}
	// base64 body lines must not exceed 72 chars.
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 72 {
			t.Errorf("line exceeds 72 chars: %q", line)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestObjectTypeCode(t *testing.T) {
	cases := map[string]string{
		"CREATE OR REPLACE PACKAGE BODY uc_grid_util":         "b",
		"create or replace package   body x":                  "b",
		"CREATE OR REPLACE PACKAGE x":                         "9",
		"CREATE OR REPLACE FUNCTION f":                        "8",
		"CREATE OR REPLACE PROCEDURE p":                       "7",
		"CREATE OR REPLACE TYPE t":                            "d",
		"CREATE OR REPLACE TYPE BODY t":                       "e",
		"CREATE OR REPLACE EDITIONABLE PACKAGE BODY type_pkg": "b",
		"CREATE OR REPLACE FUNCTION package_name":             "8",
		"something else":                                      "8",
	}
	for header, want := range cases {
		if got := objectTypeCode(header); got != want {
			t.Errorf("objectTypeCode(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestSplitBase64AvoidsLeadingSlash(t *testing.T) {
	in := strings.Repeat("A", 72) + "/" + strings.Repeat("B", 10) + "//" + strings.Repeat("C", 140) + "/"
	lines := splitBase64(in, 72)
	if strings.Join(lines, "") != in {
		t.Fatalf("lines do not rejoin to the input")
	}
	for i, l := range lines {
		if len(l) > 72 {
			t.Errorf("line %d longer than 72: %d", i, len(l))
		}
		if i > 0 && strings.HasPrefix(l, "/") {
			t.Errorf("line %d starts with '/': %q", i, l)
		}
	}
}
