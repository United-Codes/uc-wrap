package ucwrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHexLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"80 a2", true},
		{"2b 60", true},
		{"DEADBEEF cafe", true},
		{"80 a2\r", true},
		{"80 a2  ", true},
		{"80", false},          // single token
		{"", false},            // empty
		{"xy zz", false},       // non-hex
		{"80 ", false},         // empty second token
		{" 80", false},         // empty first token
		{"abc def ghi", false}, // third token is non-hex after first space split? -> "def ghi" not hex
		{"create or replace", false},
	}
	for _, tt := range tests {
		if got := isHexLine(tt.line); got != tt.want {
			t.Errorf("isHexLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestUnwrapV2NotWrapped(t *testing.T) {
	src := "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\n"
	out, err := UnwrapV2(src)
	if !errors.Is(err, ErrNotWrapped) {
		t.Fatalf("expected ErrNotWrapped, got %v", err)
	}
	if out != src {
		t.Errorf("expected input returned unchanged")
	}
}

// Golden test: unwrap a real Oracle DBMS_DDL.WRAP fixture.
func TestUnwrapV2OracleGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "oracle_wrapped_func.sql"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, err := UnwrapV2(string(data))
	if err != nil {
		t.Fatalf("UnwrapV2 error: %v", err)
	}
	want := "CREATE OR REPLACE FUNCTION uc_demo_fn(P_X IN NUMBER) RETURN NUMBER IS\n" +
		"  V_RESULT NUMBER;\n" +
		"BEGIN\n" +
		"  V_RESULT := P_X * 2 + 1;\n" +
		"  RETURN V_RESULT;\n" +
		"END;\n/\n"
	if out != want {
		t.Errorf("unwrap mismatch:\n got: %q\nwant: %q", out, want)
	}
}

func TestUnwrapV2CorruptData(t *testing.T) {
	// Valid preamble + terminator but garbage base64 payload.
	src := "CREATE OR REPLACE FUNCTION f wrapped \na000000\n1\n8\n10 10\nQUJD\n/\n"
	_, err := UnwrapV2(src)
	if err == nil {
		t.Fatal("expected an error for corrupt payload")
	}
}

func TestUnwrapV2MarkerCaseInsensitive(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("..", "testdata", "oracle_wrapped_func.sql"))
	lower := strings.Replace(string(data), "wrapped", "WRAPPED", 1)
	if _, err := UnwrapV2(lower); err != nil {
		t.Errorf("uppercase WRAPPED should still unwrap: %v", err)
	}
}
