# CLAUDE.md

Guidance for AI coding agents working in this repo.

## What this is

(Hosted at github.com/United-Codes/uc-wrap, a fork of github.com/zx97/unwrap. Go package: `ucwrap`, CLI: `uc-wrap`.)

A Go library + CLI that **wraps / unwraps / obfuscates / deobfuscates** Oracle
PL/SQL source in the **V2 format** (Oracle 10g → 23ai/26ai), i.e. the encoding
produced by Oracle's `wrap` utility and `DBMS_DDL.WRAP`.

Ported from a C++ tool at `/Users/phartenfeller/Documents/Code/_apex/unwrap`
(itself a port of Cameron Marshall's PL/SQL Unwrapper). **V1 (Oracle 8/8i/9i,
DIANA-grammar) is intentionally not ported.** The C++ source is the reference
for algorithm questions and a cross-check oracle (its `unwrap`/`wrap` binary).

## Layout

- `ucwrap/` — the library (no external deps except `golang.org/x/crypto/pbkdf2`).
  - `cipher.go` — the 256-byte substitution table `cipherFrom` + computed inverse `cipherTo`.
  - `unwrap.go` — `UnwrapV2`; base64 (lenient) + cipher + zlib inflate.
  - `wrap.go` — `WrapV2`; comment strip, header extract, uppercase-preserving-strings, zlib deflate + SHA-1 + cipher + base64 + preamble.
  - `detect.go` — `UnwrapAll` (multi-section), `ApplyCompat` (strip `schema.` prefix).
  - `obfuscate.go` — `Obfuscate`/`Deobfuscate`, identifier rename, `SplitEnc`, `WrapObfuscated`.
  - `crypto.go` — AES-256-CBC + PBKDF2-HMAC-SHA256 (100k iters) + PKCS7; wire format `salt(16)||IV(16)||ciphertext`.
  - `reserved.go` — PL/SQL reserved words the obfuscator never renames.
- `cmd/uc-wrap/main.go` — subcommand CLI (`wrap`/`obfuscate`/`deobfuscate`; there is deliberately no `unwrap` command, unwrapping is library-only and used internally by `deobfuscate`), stdin/`-i`, stdout/`-o`; `wrap`/`obfuscate` accept several inputs or a glob (`-i ./*.pkb`, shell- or self-expanded) and write each `.pkb` to `.pkw` (`runBatch`); no subcommand means `wrap`.
- `testdata/` — committed golden fixtures (real `DBMS_DDL.WRAP` output);
  `testdata/packages/` holds sample package spec+body pairs for the
  install-into-DB tests.

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

Obfuscate preserves **layout**: every line break and the indentation stay, `/* */`
comments are blanked to spaces (keeping their newlines) and `--` comments are
cut at the line break, and generated names are the **same length** as the
originals. So Oracle's error/backtrace line *and column* numbers for an
obfuscated+wrapped unit match the original file. Deobfuscate therefore restores
the comment-stripped source exactly (modulo identifier case: a local written in
several cases comes back in its first spelling). Plain wrap keeps line numbers
too (`stripComments` blanks block comments instead of deleting them). Don't add
anything that moves lines (dead code, line joining) — that would make
production debugging harder; this is a hard requirement.

## Obfuscator: rename only what's provably safe

`Obfuscate` renames **only locally-declared names** (variables, constants,
exceptions, cursors, local types), so the obfuscated unit compiles identically
to the original. `collectRenamableLocals` (in `obfuscate.go`) does a coarse
block-structure scan over `tokenizePLSQL` output: it opens a *declarative* frame
at a package/subprogram `IS`/`AS` or `DECLARE`, switches to *executable* at
`BEGIN`, and pops at `END` (while `END IF`/`LOOP`/`CASE` and bare CASE-expression
`END`s don't pop). It collects the first identifier of each declaration in a
declarative frame only — so schema objects in embedded SQL, public subprogram
names, parameters, and package-spec names are **never** renamed. Over-collection
is the only unsafe direction, so the scanner declines when unsure.

`renamableLocals` = `collectRenamableLocals` minus `collectUnsafeNames`
(`safety.go`). A declared local is **not** renamed if its spelling could also
mean something else: a member after `.` (`rec.status`, `pkg.g_cache`), a bind
`:x` / inquiry directive `$$x`, a named argument `x =>`, a procedure / function /
cursor parameter or record field name, a quoted identifier, or **any occurrence
in static SQL except an INTO target** (in SQL a column named like the variable
wins over it, so renaming the variable silently changes the statement; without
the schema we can't rule that out). Package **spec** declarations are public API
and are never collected. Declining is always the safe direction. Behavioural
proof lives in `TestPackagesObfuscatedBehaviourMatchesPlain` (DB).

`replaceIdentifiers` renames case-insensitively (mapping keyed by UPPER name) and
renames the **base** of a qualified reference but never a member after `.`
(`rec.field` → base renamed, `field` kept). The `..` range operator is special-
cased so `1..coll.count` doesn't treat `coll` as a member (that bug caused
`PLS-00201 … .COUNT must be declared`). Don't reintroduce a "copy the whole
dotted chain verbatim" shortcut — it leaves the base unrenamed and breaks
consistency for local records/collections.

All scanners share `lexemeEnd` (`lex.go`) for strings, `q'[...]'` / `nq'...'`
alternative quoting, quoted identifiers and comments — don't hand-roll quote
handling again (it broke on `q'[it's]'`). Generated names are random
(crypto/rand), same length as the original, start with a letter and contain a
digit or `_` (never a keyword), and avoid every identifier in the source.

This is a deliberate divergence from the C++ reference, whose obfuscator renames
by a protect-list and corrupts embedded SQL / public names (it does not reliably
compile). A spec and body in one file now obfuscate together (layout is kept, so
`WrapObfuscated` finds each `CREATE`); `TestPackagesObfuscateSpecAndBodyInOneFile`
covers it.

## Tests

- `go test ./...` — hermetic (unit, round-trip, golden fixtures). Keep these green and DB-free.
- `go test -tags dbtest ./ucwrap` — shells out to SQLcl (`sql`) against the
  connection `local-23ai-uc_testinstall_1` (override with `UCWRAP_SQL_CONN`).
  These prove Go unwraps real `DBMS_DDL.WRAP` output and Go-wrapped/obfuscated
  objects install and run. DB tests live in `db_integration_test.go` behind the
  `//go:build dbtest` tag.
- `packages_fixtures_test.go` defines the sample packages (`packageFixtures`):
  hand-written `UCW_*` packages under `testdata/packages/<OBJECT>.{pks,pkb}`
  (conditional compilation, local record/collection types, cursors, labels/goto,
  dynamic SQL, string literals containing comment markers; they compile cleanly
  against `DUAL` only), two packages from Oracle's APEX sample apps (`EBA_CUST_FW`,
  `EBA_DEMO_IR_PKG`), and `UCW_BIGGEN`, generated in code with 400 functions
  (~260 base64 lines) as the terminator stress case. Do **not** commit
  proprietary/customer package sources here.
- `packages_test.go` (hermetic) round-trips wrap and obfuscate for every fixture
  and pins the block scanner's renamable-locals on known packages.
- `packages_db_test.go` (`dbtest` tag) installs each fixture. The wrap test
  asserts the wrapped body's `USER_ERRORS` are byte-identical to the plain
  normalized source's (transparent wrap) and that nothing is rejected as
  malformed. The obfuscate test asserts Oracle accepts the unit **and the
  obfuscated body's error texts equal the plain source's** (the safe obfuscator
  introduces no new errors), then unwrap+deobfuscate restores every renamed
  local. Errors are compared dependency-robustly: the `EBA_*` packages reference
  objects absent in the test schema, but those errors appear equally on both sides.

To regenerate a golden fixture: `SELECT DBMS_DDL.WRAP('CREATE OR REPLACE ...')
FROM dual` via SQLcl and save the wrapped text to `testdata/`.

## Conventions

- Faithful port: function/logic names mirror the C++ where practical.
- Cross-check behavior against the C++ binary at `../unwrap/unwrap` when in doubt.
- The substitution table and reserved-word list are copied verbatim — don't edit.
