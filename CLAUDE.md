# CLAUDE.md

Guidance for AI coding agents working in this repo.

## What this is

A Go library + CLI that **wraps / unwraps / obfuscates / deobfuscates** Oracle
PL/SQL source in the **V2 format** (Oracle 10g → 23ai/26ai), i.e. the encoding
produced by Oracle's `wrap` utility and `DBMS_DDL.WRAP`.

Ported from a C++ tool at `/Users/phartenfeller/Documents/Code/_apex/unwrap`
(itself a port of Cameron Marshall's PL/SQL Unwrapper). **V1 (Oracle 8/8i/9i,
DIANA-grammar) is intentionally not ported.** The C++ source is the reference
for algorithm questions and a cross-check oracle (its `unwrap`/`wrap` binary).

## Layout

- `plsqlwrap/` — the library (no external deps except `golang.org/x/crypto/pbkdf2`).
  - `cipher.go` — the 256-byte substitution table `cipherFrom` + computed inverse `cipherTo`.
  - `unwrap.go` — `UnwrapV2`; base64 (lenient) + cipher + zlib inflate.
  - `wrap.go` — `WrapV2`; comment strip, header extract, uppercase-preserving-strings, zlib deflate + SHA-1 + cipher + base64 + preamble.
  - `detect.go` — `UnwrapAll` (multi-section), `ApplyCompat` (strip `schema.` prefix).
  - `obfuscate.go` — `Obfuscate`/`Deobfuscate`, identifier rename, `SplitEnc`, `WrapObfuscated`.
  - `crypto.go` — AES-256-CBC + PBKDF2-HMAC-SHA256 (100k iters) + PKCS7; wire format `salt(16)||IV(16)||ciphertext`.
  - `reserved.go` — PL/SQL reserved words the obfuscator never renames.
- `cmd/plsqlwrap/main.go` — subcommand CLI (`wrap`/`unwrap`/`obfuscate`/`deobfuscate`), stdin/`-i`, stdout/`-o`.
- `testdata/` — committed golden fixtures (real `DBMS_DDL.WRAP` output).

## V2 format (the essentials)

Wrap: strip comments → drop `CREATE OR REPLACE ` → truncate after last `;` +
append NUL → uppercase (object name case preserved, string literals preserved) →
zlib deflate → prepend SHA-1 of the **compressed** bytes → byte-substitute via
`cipherTo` → base64 → preamble. Unwrap reverses it.

Preamble emitted:
```
<header> wrapped \n   (note trailing space)
a000000\n
1\n                   (a version/format number; Oracle emits 369, "1" also installs)
abcd\n  x15           (literal "abcd", 15 lines — Oracle emits these verbatim too)
8\n
<ulen-hex> <lcode-hex>\n   (terminator)
<base64, wrapped at 72 chars>\n
\n/\n
```

### Terminator gotcha (important, hard-won)

The terminator's **second field** = `len(base64) + numLines - 1`
(`numLines = ceil(len(base64)/72)`) — the base64 length *including its internal
newlines*. The upstream C++ used `len(base64)+1`, which only matches for ≤2
base64 lines. **Oracle 23ai rejects a wrong value with `PLS-00753: malformed or
corrupted wrapped unit`** for larger units; 19c tolerated it. This is fixed in
`wrap.go` — do not "simplify" it back to `+1`. The first field is the
uncompressed length (including the trailing NUL).

## Known limitation

Go's `compress/flate` is not byte-identical to zlib's deflate, so Go-wrapped
output is **not** byte-for-byte equal to Oracle's `wrap`. It is still valid:
the SHA-1 is over our own compressed bytes, so Oracle installs/runs it. Don't
write tests asserting byte-equality with Oracle's wrap; assert functional
behavior instead. Unwrapping genuine Oracle output IS exact.

Obfuscate→Deobfuscate restores identifier **names** but not original formatting
(the obfuscator strips formatting; deobfuscate re-indents). Tests assert names,
not exact layout.

## Tests

- `go test ./...` — hermetic (unit, round-trip, golden fixtures). Keep these green and DB-free.
- `go test -tags dbtest ./plsqlwrap` — shells out to SQLcl (`sql`) against the
  connection `local-23ai-uc_testinstall_1` (override with `PLSQLWRAP_SQL_CONN`).
  These prove Go unwraps real `DBMS_DDL.WRAP` output and Go-wrapped/obfuscated
  objects install and run. DB tests live in `db_integration_test.go` behind the
  `//go:build dbtest` tag.

To regenerate a golden fixture: `SELECT DBMS_DDL.WRAP('CREATE OR REPLACE ...')
FROM dual` via SQLcl and save the wrapped text to `testdata/`.

## Conventions

- Faithful port: function/logic names mirror the C++ where practical.
- Cross-check behavior against the C++ binary at `../unwrap/unwrap` when in doubt.
- The substitution table and reserved-word list are copied verbatim — don't edit.
