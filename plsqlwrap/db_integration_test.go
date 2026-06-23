//go:build dbtest

// Package-level DB integration tests. Run with:
//
//	go test -tags dbtest ./plsqlwrap
//
// They shell out to SQLcl against a live Oracle instance. The connection name
// defaults to "local-23ai-uc_testinstall_1" and can be overridden with the
// PLSQLWRAP_SQL_CONN environment variable.
package plsqlwrap

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func sqlConn() string {
	if c := os.Getenv("PLSQLWRAP_SQL_CONN"); c != "" {
		return c
	}
	return "local-23ai-uc_testinstall_1"
}

// runSQL feeds a script to SQLcl over stdin and returns combined stdout/stderr.
func runSQL(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("sql", "-s", "-name", sqlConn())
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sqlcl: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("sqlcl exited with error: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("sqlcl timed out\noutput so far:\n%s", out.String())
	}
	return out.String()
}

func extractBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return strings.Trim(s[i:i+j], "\r\n")
}

// oracleWrap asks the server to wrap a full CREATE statement via DBMS_DDL.WRAP.
func oracleWrap(t *testing.T, ddl string) string {
	t.Helper()
	esc := strings.ReplaceAll(ddl, "'", "''")
	script := "set serveroutput on size unlimited\n" +
		"set feedback off\nset heading off\nset verify off\nset define off\n" +
		"declare\n  l_wrapped varchar2(32767);\nbegin\n" +
		"  l_wrapped := dbms_ddl.wrap('" + esc + "');\n" +
		"  dbms_output.put_line('<<<BEGIN>>>');\n" +
		"  dbms_output.put_line(l_wrapped);\n" +
		"  dbms_output.put_line('<<<END>>>');\nend;\n/\n"
	out := runSQL(t, script)
	w := extractBetween(out, "<<<BEGIN>>>", "<<<END>>>")
	if w == "" {
		t.Fatalf("DBMS_DDL.WRAP returned nothing; output:\n%s", out)
	}
	return w
}

// TestUnwrapAgainstOracleWrap proves Go unwrap handles genuine Oracle output.
func TestUnwrapAgainstOracleWrap(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		want []string // substrings expected in the unwrapped (uppercased) source
	}{
		{
			name: "function",
			ddl:  "CREATE OR REPLACE FUNCTION uc_t_fn(p_x IN NUMBER) RETURN NUMBER IS BEGIN RETURN p_x*2; END;",
			want: []string{"FUNCTION uc_t_fn", "RETURN P_X*2", "END;"},
		},
		{
			name: "procedure",
			ddl:  "CREATE OR REPLACE PROCEDURE uc_t_pr(p_x IN NUMBER) IS v NUMBER; BEGIN v := p_x; END;",
			want: []string{"PROCEDURE uc_t_pr", "V := P_X", "END;"},
		},
		{
			name: "deterministic",
			ddl:  "CREATE OR REPLACE FUNCTION uc_t_det(p NUMBER) RETURN NUMBER DETERMINISTIC IS BEGIN RETURN p; END;",
			want: []string{"FUNCTION uc_t_det", "DETERMINISTIC", "RETURN P;"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := oracleWrap(t, tc.ddl)
			got, err := UnwrapV2(wrapped)
			if err != nil {
				t.Fatalf("UnwrapV2: %v\nwrapped:\n%s", err, wrapped)
			}
			up := strings.ToUpper(got)
			for _, w := range tc.want {
				if !strings.Contains(up, strings.ToUpper(w)) {
					t.Errorf("unwrapped output missing %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestWrapInstallsAndRuns proves Go-wrapped output installs and behaves like the
// plain source in Oracle. It covers both a trivial body (short, <= 2 base64
// lines) and a body with a declaration section that spans 3+ base64 lines —
// the latter exercises the terminator length-field fix (PLS-00753 otherwise).
func TestWrapInstallsAndRuns(t *testing.T) {
	cases := []struct {
		fn    string
		plain string
		arg   int
		want  int
	}{
		{
			fn:    "uc_wraptest_fn",
			plain: "CREATE OR REPLACE FUNCTION uc_wraptest_fn(p_x IN NUMBER) RETURN NUMBER IS\nBEGIN\n  RETURN p_x * 3 + 7;\nEND;\n",
			arg:   10, want: 37, // 10*3+7
		},
		{
			// Declaration section + multiple statements -> 3+ base64 lines.
			fn: "uc_wraptest_decl",
			plain: "CREATE OR REPLACE FUNCTION uc_wraptest_decl(p_input IN NUMBER) RETURN NUMBER IS\n" +
				"  v_scaled NUMBER;\n  v_offset NUMBER := 100;\n" +
				"BEGIN\n  v_scaled := p_input * 5;\n  RETURN v_scaled + v_offset;\nEND;\n",
			arg: 4, want: 120, // 4*5+100
		},
	}
	for _, tc := range cases {
		t.Run(tc.fn, func(t *testing.T) {
			t.Cleanup(func() { runSQL(t, "DROP FUNCTION "+tc.fn+";\n") })
			wrapped := WrapV2(tc.plain, false, false)
			script := "set feedback off\nset define off\nset heading off\n" +
				wrapped + "\n" +
				"select 'ERRCOUNT:'||count(*) from user_errors where name='" + strings.ToUpper(tc.fn) + "';\n" +
				"select 'RESULT:'||" + tc.fn + "(" + itoa(tc.arg) + ") from dual;\n"
			out := runSQL(t, script)
			if !strings.Contains(out, "ERRCOUNT:0") {
				t.Errorf("wrapped function had compile errors:\n%s", out)
			}
			if !strings.Contains(out, "RESULT:"+itoa(tc.want)) {
				t.Errorf("wrapped function returned wrong result (want %d):\n%s", tc.want, out)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestObfuscateInstallsAndRuns proves an obfuscated+wrapped object installs,
// runs identically, and deobfuscates back to the original names.
func TestObfuscateInstallsAndRuns(t *testing.T) {
	const fn = "uc_obftest_fn"
	plain := "CREATE OR REPLACE FUNCTION " + fn + "(p_input IN NUMBER) RETURN NUMBER IS\n" +
		"  v_scaled NUMBER;\n" +
		"BEGIN\n" +
		"  v_scaled := p_input * 5;\n" +
		"  RETURN v_scaled;\nEND;\n"

	t.Cleanup(func() { runSQL(t, "DROP FUNCTION "+fn+";\n") })

	obf, err := Obfuscate(plain, "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := WrapObfuscated(obf, false)

	script := "set feedback off\nset define off\nset heading off\n" +
		wrapped + "\n" +
		"select 'ERRCOUNT:'||count(*) from user_errors where name='" + strings.ToUpper(fn) + "';\n" +
		"select 'RESULT:'||" + fn + "(4) from dual;\n"
	out := runSQL(t, script)

	if !strings.Contains(out, "ERRCOUNT:0") {
		t.Errorf("obfuscated function had compile errors:\n%s", out)
	}
	if !strings.Contains(out, "RESULT:20") { // 4*5
		t.Errorf("obfuscated function returned wrong result (want 20):\n%s", out)
	}

	// Deobfuscate: unwrap the body then restore names.
	parts := SplitEnc(wrapped)
	unwrapped := UnwrapAll(parts.Body)
	restored, err := Deobfuscate(unwrapped+"\n"+parts.Enc, "pw123")
	if err != nil {
		t.Fatalf("Deobfuscate: %v", err)
	}
	for _, name := range []string{"v_scaled", "p_input"} {
		if !strings.Contains(restored, name) {
			t.Errorf("deobfuscation did not restore %q:\n%s", name, restored)
		}
	}
}
