# plsqlwrap (Go)

A Go library and CLI to **wrap**, **unwrap**, **obfuscate** and **deobfuscate**
Oracle PL/SQL source code using the **V2 format** (Oracle 10g and later, through
23ai/26ai) — the same encoding produced by Oracle's native `wrap` utility and
`DBMS_DDL.WRAP`.

This is a Go port of the C++ "PL/SQL Unwrap" tool by **Manuel FLURY**, itself a
port of [Cameron Marshall's PL/SQL Unwrapper][original]. The legacy V1
(Oracle 8/8i/9i) path is intentionally not ported.

[original]: https://github.com/oddz/PL-SQL-Unwrapper

## Install / build

```sh
go build ./cmd/plsqlwrap
```

## CLI

```
plsqlwrap unwrap      [-i file] [-o file]
plsqlwrap wrap        [-i file] [-o file] [--keep-comments] [--compat]
plsqlwrap obfuscate   [-i file] [-o file] -p passphrase [--keep-comments]
plsqlwrap deobfuscate [-i file] [-o file] -p passphrase
```

- Reads **stdin** when `-i` is omitted; writes **stdout** when `-o` is omitted.
- `--keep-comments` keeps comments (default strips them, like Oracle's wrap).
- `--compat` strips a `schema.` prefix from object names before wrapping
  (Oracle truncates names at the dot otherwise).

Examples:

```sh
# Round-trip
plsqlwrap wrap   -i func.sql -o func.plb
plsqlwrap unwrap -i func.plb

# Obfuscate then restore
plsqlwrap obfuscate   -i pkg.sql -p s3cret -o pkg.obf.sql
plsqlwrap deobfuscate -i pkg.obf.sql -p s3cret
```

## Library

```go
import "github.com/phartenfeller/plsql-unwrap-go/plsqlwrap"

out, err := plsqlwrap.UnwrapV2(wrapped)             // single unit
out       = plsqlwrap.UnwrapAll(fileWithSections)   // every wrapped section
wrapped   = plsqlwrap.WrapV2(src, keepComments, preserveCase)
obf, err  = plsqlwrap.Obfuscate(src, passphrase, keepComments)
src, err  = plsqlwrap.Deobfuscate(obf, passphrase)
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
go test -tags dbtest ./plsqlwrap   # also runs against a live Oracle instance
```

The DB tests use SQLcl (`sql`) against the connection named
`local-23ai-uc_testinstall_1` (override with `PLSQLWRAP_SQL_CONN`). They verify
that Go unwraps real `DBMS_DDL.WRAP` output and that Go-wrapped/obfuscated
objects install and run correctly.

## License

GNU General Public License v3.0 (inherited from the upstream project). Original
PL/SQL Unwrapper by Cameron Marshall; C++ port by Manuel FLURY; Go port derived
from that work.
