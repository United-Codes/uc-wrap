# uc-wrap

A Go library and CLI to **wrap**, **obfuscate** and **deobfuscate**
Oracle PL/SQL source code using the **V2 format** (Oracle 10g and later, through
23ai/26ai) — the same encoding produced by Oracle's native `wrap` utility and
`DBMS_DDL.WRAP`.

This project is a fork of [zx97/unwrap][fork], reimplemented in Go. That tool is a
port of [Cameron Marshall's PL/SQL Unwrapper][original]. The legacy V1
(Oracle 8/8i/9i) path is intentionally not ported.

[fork]: https://github.com/zx97/unwrap
[original]: https://github.com/oddz/PL-SQL-Unwrapper

## Install / build

```sh
go build ./cmd/uc-wrap
```

## CLI

```
uc-wrap wrap        [-i file] [-o file] [--keep-comments] [--compat]
uc-wrap obfuscate   [-i file] [-o file] -p passphrase [--keep-comments]
uc-wrap deobfuscate [-i file] [-o file] -p passphrase
```

- Reads **stdin** when `-i` is omitted; writes **stdout** when `-o` is omitted.
- `--keep-comments` keeps comments (default strips them, like Oracle's wrap).
- `--compat` strips a `schema.` prefix from object names before wrapping
  (Oracle truncates names at the dot otherwise).

Examples:

```sh
# Wrap (writes func.plb)
uc-wrap wrap -i func.sql -o func.plb

# Obfuscate then restore
uc-wrap obfuscate   -i pkg.sql -p s3cret -o pkg.obf.sql
uc-wrap deobfuscate -i pkg.obf.sql -p s3cret
```

## Library

```go
import "github.com/United-Codes/uc-wrap/ucwrap"

out, err := ucwrap.UnwrapV2(wrapped)             // single unit
out       = ucwrap.UnwrapAll(fileWithSections)   // every wrapped section
wrapped   = ucwrap.WrapV2(src, keepComments, preserveCase)
obf, err  = ucwrap.Obfuscate(src, passphrase, keepComments)
src, err  = ucwrap.Deobfuscate(obf, passphrase)
```

## How wrapping works (V2)

`wrap`: strip comments → drop `CREATE OR REPLACE ` prefix → truncate after last
`;` + NUL → uppercase (object name case preserved) → zlib deflate → prepend
SHA-1 of the compressed bytes → byte-substitution cipher → base64 → preamble +
`<hex> <hex>` terminator + 72-char base64 lines. `unwrap` reverses each step.

**Note:** Go's deflate is not byte-identical to zlib's, so Go-wrapped output is
not byte-for-byte equal to Oracle's own `wrap`. It is still fully valid: the
embedded SHA-1 matches our own compressed bytes, so Oracle installs and runs it
correctly. Unwrapping genuine Oracle output is exact.

## Tests

```sh
go test ./...                  # hermetic: unit, round-trip, golden fixtures
go test -tags dbtest ./ucwrap   # also runs against a live Oracle instance
```

The DB tests use SQLcl (`sql`) against the connection named
`local-23ai-uc_testinstall_1` (override with `UCWRAP_SQL_CONN`). They verify
that Go unwraps real `DBMS_DDL.WRAP` output and that Go-wrapped/obfuscated
objects install and run correctly.

## License

GNU General Public License v3.0 (inherited from the upstream project). Original
PL/SQL Unwrapper by Cameron Marshall; C++ port by Manuel FLURY; Go port derived
from that work.
