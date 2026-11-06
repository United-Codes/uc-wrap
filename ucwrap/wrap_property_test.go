package ucwrap

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// genBody builds a pseudo-random, already-normalized (uppercase, no comments)
// package body with n statements.
func genBody(r *rand.Rand, name string, n int) string {
	const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	word := func(l int) string {
		b := make([]byte, l)
		for i := range b {
			b[i] = alnum[r.Intn(len(alnum))]
		}
		return string(b)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE OR REPLACE PACKAGE BODY %s AS\n", name)
	for i := 0; i < n; i++ {
		switch r.Intn(3) {
		case 0:
			fmt.Fprintf(&sb, "  V_%s NUMBER := %d;\n", word(6), r.Intn(1000000))
		case 1:
			fmt.Fprintf(&sb, "  C_%s CONSTANT VARCHAR2(100) := '%s';\n", word(5), word(1+r.Intn(60)))
		default:
			fmt.Fprintf(&sb, "  PROCEDURE P_%s IS BEGIN NULL; END P_%s;\n", word(7), word(3))
		}
	}
	sb.WriteString("END " + name + ";\n")
	return sb.String()
}

// naiveLeadingSlash reports whether a plain fixed-width split of b64 would
// produce a continuation line starting with '/'.
func naiveLeadingSlash(b64 string, width int) bool {
	for i := width; i < len(b64); i += width {
		if b64[i] == '/' {
			return true
		}
	}
	return false
}

func TestWrapV2StructuralInvariants(t *testing.T) {
	const width = 72
	r := rand.New(rand.NewSource(20240607))

	// statement counts: tiny, near line-width boundaries, and a few hundred lines
	var sizes []int
	for n := 0; n <= 12; n++ {
		sizes = append(sizes, n)
	}
	for i := 0; i < 400; i++ {
		sizes = append(sizes, 1+r.Intn(1500))
	}

	naiveHits := 0
	for caseNo, n := range sizes {
		name := fmt.Sprintf("PKG_%d", caseNo)
		src := genBody(r, name, n)
		out := WrapV2(src, false, false)
		ctx := fmt.Sprintf("case %d (n=%d)", caseNo, n)

		lines := strings.Split(out, "\n")
		// header (1) + a000000 + 1 + 15 abcd = 18 lines, then type code, then terminator
		if len(lines) < 22 {
			t.Fatalf("%s: output too short (%d lines)", ctx, len(lines))
		}
		if lines[18] != "b" {
			t.Fatalf("%s: line 19 type code = %q, want b", ctx, lines[18])
		}
		term := strings.Fields(lines[19])
		if len(term) != 2 {
			t.Fatalf("%s: bad terminator line %q", ctx, lines[19])
		}
		// out ends with "\n/\n": split gives [..., last b64, "", "/", ""]
		if lines[len(lines)-1] != "" || lines[len(lines)-2] != "/" || lines[len(lines)-3] != "" {
			t.Fatalf("%s: output does not end with blank line + lone '/'", ctx)
		}
		b64Lines := lines[20 : len(lines)-3]
		if len(b64Lines) == 0 {
			t.Fatalf("%s: no base64 lines", ctx)
		}
		for i, l := range b64Lines {
			if l == "" {
				t.Fatalf("%s: blank line inside base64 block at index %d", ctx, i)
			}
			if strings.HasPrefix(l, "/") {
				t.Fatalf("%s: base64 line %d starts with '/'", ctx, i)
			}
			if len(l) > width {
				t.Fatalf("%s: base64 line %d has %d chars (> %d)", ctx, i, len(l), width)
			}
		}
		block := strings.Join(b64Lines, "\n")
		lcode, err := strconv.ParseInt(term[1], 16, 64)
		if err != nil || int(lcode) != len(block) {
			t.Fatalf("%s: terminator second field %q (=%d), base64 block length %d", ctx, term[1], lcode, len(block))
		}
		joined := strings.Join(b64Lines, "")
		if _, err := base64.StdEncoding.DecodeString(joined); err != nil {
			t.Fatalf("%s: base64 does not decode: %v", ctx, err)
		}

		got, err := UnwrapV2(out)
		if err != nil {
			t.Fatalf("%s: UnwrapV2 failed: %v", ctx, err)
		}
		if want := src + "/\n"; got != want {
			t.Fatalf("%s: round trip mismatch\n got: %.200q\nwant: %.200q", ctx, got, want)
		}

		if naiveLeadingSlash(joined, width) {
			naiveHits++
		}
	}
	if naiveHits == 0 {
		t.Fatalf("generator too weak: a naive %d-column split never produced a leading '/'", width)
	}
	t.Logf("%d cases, %d would have had a leading '/' with a naive split", len(sizes), naiveHits)
}
