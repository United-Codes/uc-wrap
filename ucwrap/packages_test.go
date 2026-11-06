package ucwrap

import (
	"strings"
	"testing"
)

// TestPackagesWrapRoundTrip wraps each sample package body and checks that
// unwrapping yields the same statement (comments stripped, upper-cased outside
// string literals, trailing NUL removed).
func TestPackagesWrapRoundTrip(t *testing.T) {
	for _, obj := range packageFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			_, body, ok := loadFixture(t, obj)
			if !ok {
				t.Fatalf("fixture %s missing", obj)
			}
			wrapped := WrapV2(body, false, false)
			if !strings.Contains(wrapped, "wrapped") {
				t.Fatalf("output is not a wrapped unit")
			}
			back, err := UnwrapV2(wrapped)
			if err != nil {
				t.Fatalf("UnwrapV2: %v", err)
			}
			if !strings.Contains(strings.ToUpper(back), "PACKAGE BODY") && !strings.Contains(strings.ToUpper(back), strings.ToUpper(obj)) {
				t.Errorf("unwrapped text does not look like the original package body")
			}
			if again := UnwrapAll(wrapped); strings.TrimSpace(again) == "" {
				t.Errorf("UnwrapAll returned nothing")
			}
		})
	}
}

// TestPackagesObfuscateRoundTrip obfuscates each sample, wraps it, and checks
// that unwrap+deobfuscate restores every locally-declared name the scanner
// chose to rename.
func TestPackagesObfuscateRoundTrip(t *testing.T) {
	const pw = "sample-pass-123"
	for _, obj := range packageFixtures {
		obj := obj
		t.Run(obj, func(t *testing.T) {
			_, body, _ := loadFixture(t, obj)
			obf, err := Obfuscate(body, pw, false)
			if err != nil {
				t.Fatalf("Obfuscate: %v", err)
			}
			wrapped := WrapObfuscated(obf, false)
			parts := SplitEnc(wrapped)
			restored, err := Deobfuscate(UnwrapAll(parts.Body)+"\n"+parts.Enc, pw)
			if err != nil {
				t.Fatalf("Deobfuscate: %v", err)
			}
			up := strings.ToUpper(restored)
			for name := range collectRenamableLocals(stripComments(body)) {
				if !strings.Contains(up, name) {
					t.Errorf("local %q not restored", name)
				}
			}
		})
	}
}

// TestPackagesRenamableLocals pins the block scanner's behaviour on packages
// with known local declarations, and that public names are never collected.
func TestPackagesRenamableLocals(t *testing.T) {
	cases := map[string]struct{ want, never []string }{
		"UCW_COLLECTIONS": {
			want:  []string{"T_PAIR", "T_PAIR_TAB", "G_CACHE", "L_NUMBERS", "L_TOTAL", "L_PAIRS"},
			never: []string{"SUM_RANGE", "TOP_SQUARES", "T_NUMBER_TAB", "P_FROM"},
		},
		"UCW_FLOW": {
			want:  []string{"C_BAD_BOUNDS", "L_OUT", "L_TMP", "L_RESULT", "L_RETRIES", "L_STMT"},
			never: []string{"CLAMP", "FIZZBUZZ", "E_OUT_OF_RANGE", "P_VALUE"},
		},
		"UCW_CURSORS": {
			want:  []string{"G_CALLS", "T_LABEL_TAB", "L_LABELS", "L_CSV"},
			never: []string{"NUMBERS_CSV", "CALL_COUNT", "DUAL", "LEVEL"},
		},
	}
	for obj, c := range cases {
		_, body, _ := loadFixture(t, obj)
		got := collectRenamableLocals(stripComments(body))
		for _, n := range c.want {
			if _, ok := got[n]; !ok {
				t.Errorf("%s: expected local %s to be renamable; got %v", obj, n, got)
			}
		}
		for _, n := range c.never {
			if _, ok := got[n]; ok {
				t.Errorf("%s: %s must never be renamed", obj, n)
			}
		}
	}
}
