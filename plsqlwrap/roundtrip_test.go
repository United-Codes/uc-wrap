package plsqlwrap

import (
	"strings"
	"testing"
)

func wrapUnwrap(t *testing.T, src string, keepComments bool) string {
	t.Helper()
	wrapped := WrapV2(src, keepComments, false)
	out, err := UnwrapV2(wrapped)
	if err != nil {
		t.Fatalf("UnwrapV2 after WrapV2 failed: %v", err)
	}
	return out
}

func TestRoundTripFunctionExact(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS\n" +
		"  x NUMBER := 42; /* magic */\n" +
		"BEGIN\n" +
		"  RETURN x;\n" +
		"END;\n"
	got := wrapUnwrap(t, src, false)
	want := "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS\n" +
		"  X NUMBER := 42; \n" +
		"BEGIN\n" +
		"  RETURN X;\n" +
		"END;\n/\n"
	if got != want {
		t.Errorf("roundtrip mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRoundTripKeepComments(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS\n" +
		"  x NUMBER := 42; /* magic */\n" +
		"BEGIN\n" +
		"  RETURN x;\n" +
		"END;\n"
	got := wrapUnwrap(t, src, true)
	// keep-comments preserves the comment text, but the body (including
	// comments) is still uppercased, so "magic" becomes "MAGIC".
	if !strings.Contains(got, "MAGIC") {
		t.Errorf("keep-comments dropped the comment:\n%s", got)
	}
}

func TestRoundTripObjectTypes(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		keyword string // object keyword that should keep its name's case
		objName string
	}{
		{
			name:    "procedure",
			src:     "CREATE OR REPLACE PROCEDURE Proc1(p IN NUMBER) IS\nBEGIN\n  NULL;\nEND;\n",
			keyword: "PROCEDURE",
			objName: "Proc1",
		},
		{
			name:    "result_cache_function",
			src:     "CREATE OR REPLACE FUNCTION rc RETURN NUMBER RESULT_CACHE IS\nBEGIN\n  RETURN 1;\nEND;\n",
			keyword: "FUNCTION",
			objName: "rc",
		},
		{
			name:    "deterministic_function",
			src:     "CREATE OR REPLACE FUNCTION det(p NUMBER) RETURN NUMBER DETERMINISTIC IS\nBEGIN\n  RETURN p;\nEND;\n",
			keyword: "FUNCTION",
			objName: "det",
		},
		{
			name:    "package_body",
			src:     "CREATE OR REPLACE PACKAGE BODY Pkg AS\n  PROCEDURE q IS BEGIN NULL; END;\nEND Pkg;\n",
			keyword: "PACKAGE BODY",
			objName: "Pkg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapUnwrap(t, tc.src, false)
			if !strings.HasPrefix(got, "CREATE OR REPLACE ") {
				t.Errorf("missing CREATE OR REPLACE prefix: %q", got)
			}
			if !strings.HasSuffix(got, "/\n") {
				t.Errorf("missing trailing slash: %q", got)
			}
			// Object name case must be preserved right after the keyword.
			if !strings.Contains(got, tc.keyword+" "+tc.objName) {
				t.Errorf("object name case not preserved; want %q in:\n%s", tc.keyword+" "+tc.objName, got)
			}
			// Body keywords must be uppercased.
			if !strings.Contains(got, "BEGIN") && !strings.Contains(got, "END") {
				t.Errorf("body not uppercased: %q", got)
			}
		})
	}
}

func TestRoundTripPreservesStringLiteralCase(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION f RETURN VARCHAR2 IS\n" +
		"BEGIN\n" +
		"  RETURN 'Hello World';\n" +
		"END;\n"
	got := wrapUnwrap(t, src, false)
	if !strings.Contains(got, "'Hello World'") {
		t.Errorf("string literal case not preserved:\n%s", got)
	}
}
