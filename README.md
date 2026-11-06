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

Download a release archive (`uc-wrap_<os>_<arch>.tar.gz`, or `.zip` on Windows)
from the [releases page](https://github.com/United-Codes/uc-wrap/releases) and
verify it against `checksums.txt`. In a GitHub Action:

```yaml
- run: |
    curl -fsSLO https://github.com/United-Codes/uc-wrap/releases/latest/download/uc-wrap_linux_amd64.tar.gz
    curl -fsSLO https://github.com/United-Codes/uc-wrap/releases/latest/download/checksums.txt
    grep ' uc-wrap_linux_amd64.tar.gz$' checksums.txt | sha256sum -c -
    tar -xzf uc-wrap_linux_amd64.tar.gz uc-wrap
```

Or build from source:

```sh
go build ./cmd/uc-wrap
```

## CLI

```
uc-wrap [wrap]      [-i file|glob...] [-o file] [--keep-comments] [--compat]
uc-wrap obfuscate   [-i file|glob...] [-o file] -p passphrase [--keep-comments]
uc-wrap deobfuscate [-i file] [-o file] -p passphrase
```

- Reads **stdin** when `-i` is omitted; writes **stdout** when `-o` is omitted.
- **Exception:** `wrap` and `obfuscate` with `-i name.pkb` and no `-o` write
  `name.pkw` next to the input. Use `-o -` to force stdout.
- **Several files / globs:** `wrap` and `obfuscate` accept many inputs, e.g.
  `-i ./*.pkb`. Each `.pkb` is written to its own `.pkw`. Your shell expands the
  glob; a quoted pattern (`-i "src/*.pkb"`) is expanded by `uc-wrap` itself, which
  also covers Windows `cmd`. `-o` can't be combined with several inputs, and every
  input must be a `.pkb` file. A failing file doesn't stop the others; the exit
  code is non-zero if any failed. Globs are not recursive (`*` matches within one
  directory).
- Without a subcommand, `wrap` is assumed (`uc-wrap -i ./*.pkb`).
- `--keep-comments` keeps comments (default strips them, like Oracle's wrap).
- `--compat` strips a `schema.` prefix from object names before wrapping
  (Oracle truncates names at the dot otherwise).

Examples:

```sh
# Wrap a package body: writes my_pkg.pkw next to my_pkg.pkb (no -o needed)
uc-wrap wrap -i my_pkg.pkb

# Wrap every .pkb in the directory: each becomes <name>.pkw
uc-wrap -i ./*.pkb

# Same, quoted so uc-wrap expands the glob itself (also works on Windows cmd)
uc-wrap wrap -i "src/*.pkb"

# Same, with an explicit output file (required for non-.pkb inputs)
uc-wrap wrap -i func.sql -o func.plb

# Obfuscate (writes my_pkg.pkw), then restore
uc-wrap obfuscate   -i my_pkg.pkb -p s3cret
uc-wrap deobfuscate -i my_pkg.pkw -p s3cret

# Obfuscate a whole directory
uc-wrap obfuscate -i ./*.pkb -p s3cret
```

## What obfuscating does

`wrap` alone is not strong protection: anyone can unwrap Oracle's V2 format
(that is how this tool's own `deobfuscate` works), and the unwrapped text still
has your readable identifier names. `obfuscate` adds a layer on top:

1. **Renames private local names** (variables, constants, exceptions, cursors
   and local types) to random identifiers, consistently across the unit.
2. **Drops comments** (unless `--keep-comments`).
3. **Wraps** the result in the usual V2 format, so it installs and runs in Oracle
   like any wrapped unit.
4. **Appends an encrypted name map** (`--ENC:` block, AES-256-CBC with a key
   derived from your passphrase via PBKDF2). Without the passphrase the original
   names can't be recovered; with it, `deobfuscate` unwraps the unit and puts the
   names back.

**It does not make debugging harder.** Line breaks and indentation are kept, and
every generated name has the same length as the original, so the line *and
column* numbers in Oracle's error messages, `USER_ERRORS` and error backtraces
for the obfuscated unit are the same as for your original source. Subprogram
names still appear in call stacks and profiler output. Plain `wrap` keeps line
numbers too (comments are blanked, not deleted).

**It only renames what is provably safe**, so the unit behaves exactly like the
original. Never touched: package-spec declarations, public and private
subprogram names, parameters, record fields, schema objects, anything in a
string literal, and any local whose name also appears in static SQL (a table
column with the same name would win over the variable there, so renaming the
variable could silently change the query), after a `.`, as a named argument,
bind or `$$` directive. An unwrapped obfuscated unit therefore still reveals its
public API, subprogram names and SQL, just not the names of most internal
variables.

Limits to be aware of:

- `deobfuscate` restores the code but not the comments (they are removed), and a
  local written in several letter cases comes back in its first spelling.
- Obfuscation is an extra obstacle, not encryption of the code itself.
- The passphrase is the only secret. Keep it out of the repo and out of shell
  history where it matters. If it is lost, the names are unrecoverable.

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
