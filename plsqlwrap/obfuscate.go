package plsqlwrap

import (
	"encoding/base64"
	"errors"
	"sort"
	"strings"
)

// ErrNoEncData is returned by Deobfuscate when the source has no embedded
// "--ENC:" obfuscation block.
var ErrNoEncData = errors.New("plsqlwrap: no --ENC: obfuscation data found")

// ErrDecrypt is returned when the embedded mapping cannot be decrypted (most
// commonly a wrong passphrase).
var ErrDecrypt = errors.New("plsqlwrap: decryption failed (wrong passphrase?)")

// Obfuscate renames local identifiers to very short names, strips comments
// (unless keepComments is set) and formatting, and appends an AES-256-CBC
// encrypted name mapping as a trailing "--ENC:" block. The result can be
// recovered with Deobfuscate and the same passphrase.
//
// Object names (on CREATE lines), all package-spec identifiers, and
// procedure/function parameter names are protected from renaming.
func Obfuscate(source, passphrase string, keepComments bool) (string, error) {
	clean := source
	if !keepComments {
		clean = stripComments(source)
	}

	// Collision-avoidance set for generated short names, seeded with the global
	// reserved words plus header / package-spec / parameter identifiers (so a
	// generated short name can never shadow a public name) plus every identifier
	// already present in the source (via allIDs below).
	reserved := make(map[string]struct{}, len(reservedWords))
	for w := range reservedWords {
		reserved[w] = struct{}{}
	}
	protectHeaderIdentifiers(clean, reserved)
	protectPackageSpec(clean, reserved)
	protectParameters(clean, reserved)
	allIDs := collectAllIdentifiers(clean)

	// Only rename names we can prove are locally declared (variables, constants,
	// exceptions, cursors, local types). Everything else — schema objects
	// referenced in embedded SQL, public subprogram names, parameters,
	// package-spec names — is left untouched so the obfuscated unit still
	// compiles identically to the original.
	locals := collectRenamableLocals(clean)
	freq, orig := countLocalOccurrences(clean, locals)
	mapping := makeMapping(freq, allIDs, reserved) // UPPER(name) -> short

	obfuscated := clean
	if len(mapping) > 0 {
		obfuscated = replaceIdentifiers(clean, mapping)
	}
	obfuscated = stripFormatting(obfuscated)

	// Reverse map: UPPER(short) -> original spelling. Identifier matching is
	// case-insensitive, so a single uppercased key suffices.
	reverse := make(map[string]string, len(mapping))
	for up, short := range mapping {
		reverse[strings.ToUpper(short)] = orig[up]
	}
	mapStr := mappingToString(reverse)
	if mapStr == "" {
		mapStr = " " // ensure >= 1 byte for AES
	}

	encrypted, err := aesEncrypt([]byte(mapStr), passphrase)
	if err != nil {
		return "", err
	}

	encB64 := base64.StdEncoding.EncodeToString(encrypted)
	var sb strings.Builder
	sb.WriteString(obfuscated)
	sb.WriteString("\n--ENC:\n")
	for i := 0; i < len(encB64); i += 72 {
		end := i + 72
		if end > len(encB64) {
			end = len(encB64)
		}
		sb.WriteString(encB64[i:end])
		sb.WriteByte('\n')
	}
	sb.WriteString("--ENCEND\n")
	return sb.String(), nil
}

// Deobfuscate restores original identifier names from an obfuscated source
// using the embedded encrypted mapping and the given passphrase, then
// re-indents the result. Formatting is not restored exactly — only identifier
// names and a minimal BEGIN/END indentation are recovered.
func Deobfuscate(source, passphrase string) (string, error) {
	encStart := strings.LastIndex(source, "--ENC:\n")
	if encStart < 0 {
		return "", ErrNoEncData
	}
	end := strings.Index(source[encStart:], "--ENCEND")
	if end < 0 {
		return "", ErrNoEncData
	}
	end += encStart

	b64Start := strings.IndexByte(source[encStart+5:], '\n')
	if b64Start < 0 {
		return "", ErrNoEncData
	}
	b64Start += encStart + 5 + 1
	b64Data := source[b64Start:end]

	encrypted, err := decodeBase64Lenient(b64Data)
	if err != nil || len(encrypted) == 0 {
		return "", ErrCorruptData
	}

	decrypted, err := aesDecrypt(encrypted, passphrase)
	if err != nil {
		return "", ErrDecrypt
	}

	reverseMap := stringToMapping(string(decrypted))

	body := strings.TrimRight(source[:encStart], "\n\r \t")
	result := replaceIdentifiers(body, reverseMap)
	return reindentPLSQL(result), nil
}

// EncParts holds an obfuscated source split into its plain/wrapped body and its
// trailing "--ENC:" block.
type EncParts struct {
	Body string // everything before the --ENC: marker
	Enc  string // the --ENC: ... --ENCEND block (empty if absent)
}

// SplitEnc separates source at the last "--ENC:\n" marker.
func SplitEnc(source string) EncParts {
	idx := strings.LastIndex(source, "--ENC:\n")
	if idx < 0 {
		return EncParts{Body: source}
	}
	return EncParts{Body: source[:idx], Enc: source[idx:]}
}

// WrapObfuscated takes the output of Obfuscate and wraps each CREATE unit in the
// body (preserving case, since the source is already final), leaving the
// trailing --ENC: block untouched at the end. This mirrors the reference CLI's
// obfuscate orchestration.
func WrapObfuscated(obf string, keepComments bool) string {
	parts := SplitEnc(obf)
	if parts.Enc == "" {
		return WrapV2(obf, keepComments, true)
	}

	var out strings.Builder
	body := parts.Body
	up := strings.ToUpper(body)
	cursor := 0
	for {
		cr := strings.Index(up[cursor:], "CREATE")
		if cr < 0 {
			out.WriteString(body[cursor:])
			break
		}
		cr += cursor
		unitStart := cr
		for unitStart > cursor && body[unitStart-1] != '\n' {
			unitStart--
		}
		unitEnd := len(body)
		nextSearch := cr + 1
		for nextSearch <= len(body) {
			nc := strings.Index(up[nextSearch:], "CREATE")
			if nc < 0 {
				break
			}
			nc += nextSearch
			if nc == 0 || body[nc-1] == '\n' {
				unitEnd = nc
				break
			}
			nextSearch = nc + 1
		}
		out.WriteString(body[cursor:unitStart])
		out.WriteString(WrapV2(body[unitStart:unitEnd], keepComments, true))
		cursor = unitEnd
	}
	out.WriteString("\n")
	out.WriteString(parts.Enc)
	return out.String()
}

// ------------------------------------------------------------------
// Tokenizer helpers
// ------------------------------------------------------------------

func isIDChar(c byte) bool {
	return isAlnum(c) || c == '_' || c == '$' || c == '#'
}

func isIDStart(c byte) bool {
	return isAlpha(c) || c == '_'
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// tkn is a coarse PL/SQL token used by the declaration scanner.
type tkn struct {
	kind byte   // 'i' identifier, 'n' number, 's' symbol (1–2 chars), 'q' string/quoted-ident
	text string // original text
	up   string // uppercased text (identifiers only)
}

// tokenizePLSQL splits s into coarse tokens, discarding whitespace and comments
// and treating string literals and quoted identifiers as opaque.
func tokenizePLSQL(s string) []tkn {
	var toks []tkn
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			st := i
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			toks = append(toks, tkn{kind: 'q', text: s[st:i]})
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
		case c == '"':
			st := i
			i++
			for i < len(s) && s[i] != '"' {
				i++
			}
			if i < len(s) {
				i++
			}
			toks = append(toks, tkn{kind: 'q', text: s[st:i]})
		case isDigit(c):
			st := i
			for i < len(s) && (isIDChar(s[i]) || s[i] == '.') {
				i++
			}
			toks = append(toks, tkn{kind: 'n', text: s[st:i]})
		case isIDStart(c):
			st := i
			for i < len(s) && isIDChar(s[i]) {
				i++
			}
			w := s[st:i]
			toks = append(toks, tkn{kind: 'i', text: w, up: strings.ToUpper(w)})
		case c == ':' && i+1 < len(s) && s[i+1] == '=':
			toks = append(toks, tkn{kind: 's', text: ":="})
			i += 2
		default:
			toks = append(toks, tkn{kind: 's', text: string(c)})
			i++
		}
	}
	return toks
}

// collectRenamableLocals returns the set of UPPER-cased identifier names that
// are SAFE to rename: names declared in a declarative region (after IS/AS or
// DECLARE, before BEGIN) as variables, constants, exceptions, cursors, or
// types. Public subprogram names, parameters, package-spec identifiers, and —
// crucially — everything inside embedded SQL or executable code is excluded, so
// renaming them can never change which schema object a statement resolves to.
//
// The scanner tracks block nesting (subprogram/package/anonymous-block bodies
// open a declarative frame at IS/AS/DECLARE and switch to executable at BEGIN;
// END pops a frame, while END IF/LOOP/CASE and bare CASE-expression ENDs do
// not). When in doubt it declines to collect — over-collection (which could
// rename a schema name) is the only unsafe direction.
func collectRenamableLocals(s string) map[string]struct{} {
	toks := tokenizePLSQL(s)
	locals := make(map[string]struct{})

	type frame struct{ inDecl bool }
	var frames []frame
	inDecl := func() bool { return len(frames) > 0 && frames[len(frames)-1].inDecl }

	expectIS := false   // a PACKAGE/PROCEDURE/FUNCTION header is open; next top-level IS/AS opens a decl frame
	atStmtStart := true // are we at the first token of a declaration item?
	parenDepth := 0
	caseDepth := 0

	add := func(t tkn) {
		if t.kind != 'i' || len(t.text) <= 2 || isDigit(t.text[0]) || isReserved(t.text) {
			return
		}
		locals[t.up] = struct{}{}
	}

	for j := 0; j < len(toks); j++ {
		t := toks[j]
		if t.kind == 's' {
			switch t.text {
			case "(":
				parenDepth++
			case ")":
				if parenDepth > 0 {
					parenDepth--
				}
			case ";":
				if parenDepth == 0 {
					expectIS = false
					atStmtStart = inDecl()
				}
			}
			continue
		}
		if t.kind != 'i' {
			continue
		}
		switch t.up {
		case "PACKAGE", "PROCEDURE", "FUNCTION":
			expectIS = true
			atStmtStart = false
		case "DECLARE":
			frames = append(frames, frame{inDecl: true})
			atStmtStart = true
		case "IS", "AS":
			if expectIS && parenDepth == 0 {
				frames = append(frames, frame{inDecl: true})
				expectIS = false
				atStmtStart = true
			}
		case "BEGIN":
			if len(frames) > 0 && frames[len(frames)-1].inDecl {
				frames[len(frames)-1].inDecl = false
			} else {
				frames = append(frames, frame{inDecl: false})
			}
			atStmtStart = false
		case "CASE":
			caseDepth++
			atStmtStart = false
		case "END":
			var nxt tkn
			if j+1 < len(toks) {
				nxt = toks[j+1]
			}
			switch {
			case nxt.kind == 'i' && (nxt.up == "IF" || nxt.up == "LOOP"):
				// control-structure end, not a scope
			case nxt.kind == 'i' && nxt.up == "CASE":
				if caseDepth > 0 {
					caseDepth--
				}
			case caseDepth > 0:
				caseDepth-- // bare END closing a CASE expression
			default:
				if len(frames) > 0 {
					frames = frames[:len(frames)-1]
				}
			}
			atStmtStart = false
		default:
			if inDecl() && parenDepth == 0 && atStmtStart {
				switch t.up {
				case "CURSOR", "TYPE", "SUBTYPE":
					if j+1 < len(toks) && toks[j+1].kind == 'i' {
						add(toks[j+1])
					}
				case "PRAGMA":
					// skip the whole pragma
				default:
					// A variable/constant/exception declaration: t is the
					// declared name iff the next token introduces a type
					// (another identifier, or the CONSTANT/EXCEPTION keyword).
					if j+1 < len(toks) && toks[j+1].kind == 'i' {
						add(t)
					}
				}
				atStmtStart = false
			}
		}
	}
	return locals
}

// countLocalOccurrences counts, per UPPER-cased local name, how often it occurs
// as a standalone (non-member) identifier, and records the first original
// spelling seen. The frequency drives short-name assignment; the spelling is
// the deobfuscation target.
func countLocalOccurrences(s string, locals map[string]struct{}) (map[string]int, map[string]string) {
	freq := make(map[string]int)
	orig := make(map[string]string)
	var prevSig byte
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			prevSig = '\''
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				i++
			}
			prevSig = 0
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			prevSig = 0
		case c == '"':
			i++
			for i < len(s) && s[i] != '"' {
				i++
			}
			if i < len(s) {
				i++
			}
			prevSig = '"'
		case isIDStart(c):
			st := i
			for i < len(s) && isIDChar(s[i]) {
				i++
			}
			if prevSig != '.' {
				up := strings.ToUpper(s[st:i])
				if _, ok := locals[up]; ok {
					freq[up]++
					if _, seen := orig[up]; !seen {
						orig[up] = s[st:i]
					}
				}
			}
			prevSig = 'a'
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '.':
			if prevSig == '.' { // second dot of a '..' range operator
				prevSig = 0
			} else {
				prevSig = '.'
			}
			i++
		default:
			prevSig = c
			i++
		}
	}
	return freq, orig
}

// collectAllIdentifiers returns the set of every identifier in s (uppercased),
// used to avoid generating short names that collide with existing ones.
func collectAllIdentifiers(s string) map[string]struct{} {
	all := make(map[string]struct{})
	i := 0
	for i < len(s) {
		if isIDStart(s[i]) {
			start := i
			for i < len(s) && isIDChar(s[i]) {
				i++
			}
			all[strings.ToUpper(s[start:i])] = struct{}{}
		} else {
			i++
		}
	}
	return all
}

// makeMapping assigns short names to identifiers, ranking by frequency (desc),
// then length (desc), then name (desc), and skipping any short name that
// collides with a reserved word or an existing identifier.
func makeMapping(freq map[string]int, allIDs, reserved map[string]struct{}) map[string]string {
	type kv struct {
		name  string
		count int
	}
	sorted := make([]kv, 0, len(freq))
	for name, count := range freq {
		sorted = append(sorted, kv{name, count})
	}
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].count != sorted[b].count {
			return sorted[a].count > sorted[b].count
		}
		if len(sorted[a].name) != len(sorted[b].name) {
			return len(sorted[a].name) > len(sorted[b].name)
		}
		return sorted[a].name > sorted[b].name
	})

	mapping := make(map[string]string, len(sorted))
	idx := 0
	for _, e := range sorted {
		var short string
		for {
			short = shortName(idx)
			idx++
			up := strings.ToUpper(short)
			_, isRes := reserved[up]
			_, isID := allIDs[up]
			if !isRes && !isID {
				break
			}
		}
		mapping[e.name] = short
	}
	return mapping
}

// shortName converts idx to a short identifier the same way the reference does:
// append 'a'+(n%26) least-significant first, dividing by 26 each step.
func shortName(idx int) string {
	var b []byte
	n := idx
	for {
		b = append(b, byte('a'+(n%26)))
		n /= 26
		if n == 0 {
			break
		}
	}
	return string(b)
}

// replaceIdentifiers rewrites identifiers per mapping (keyed by UPPER-cased
// name, so matching is case-insensitive) while copying strings, comments and
// quoted identifiers verbatim. An identifier whose immediately preceding
// significant character is '.' is a member/qualified reference (pkg.func,
// rec.field) and is left untouched; the BASE of such a reference is still
// renamed when it is a mapped local, which keeps renaming consistent.
func replaceIdentifiers(s string, mapping map[string]string) string {
	var out strings.Builder
	out.Grow(len(s))
	var prevSig byte // last non-whitespace byte consumed from the source
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			out.WriteByte(c)
			i++
			for i < len(s) {
				out.WriteByte(s[i])
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i++
						out.WriteByte(s[i])
					} else {
						i++
						break
					}
				}
				i++
			}
			prevSig = '\''
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			out.WriteByte(c)
			out.WriteByte(s[i+1])
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				out.WriteByte(s[i])
				i++
			}
			prevSig = 0
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			out.WriteByte(c)
			out.WriteByte(s[i+1])
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				out.WriteByte(s[i])
				i++
			}
			if i+1 < len(s) {
				out.WriteByte(s[i])
				out.WriteByte(s[i+1])
				i += 2
			}
			prevSig = 0
		case c == '"':
			out.WriteByte(c)
			i++
			for i < len(s) && s[i] != '"' {
				out.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				out.WriteByte(s[i])
				i++
			}
			prevSig = '"'
		case isIDStart(c):
			start := i
			for i < len(s) && isIDChar(s[i]) {
				i++
			}
			word := s[start:i]
			if prevSig == '.' {
				out.WriteString(word) // member access — never rename
			} else if repl, ok := mapping[strings.ToUpper(word)]; ok {
				out.WriteString(repl)
			} else {
				out.WriteString(word)
			}
			prevSig = 'a'
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			out.WriteByte(c)
			i++
		case c == '.':
			out.WriteByte(c)
			// The second dot of a '..' range operator must not mark the next
			// identifier as a member (it's an operand, e.g. 1..coll.count).
			if prevSig == '.' {
				prevSig = 0
			} else {
				prevSig = '.'
			}
			i++
		default:
			out.WriteByte(c)
			prevSig = c
			i++
		}
	}
	return out.String()
}

// ------------------------------------------------------------------
// Identifier protection passes
// ------------------------------------------------------------------

// protectHeaderIdentifiers adds every identifier appearing on a CREATE header
// line (other than CREATE/OR/REPLACE) to the protected set.
func protectHeaderIdentifiers(clean string, reserved map[string]struct{}) {
	up := strings.ToUpper(clean)
	pos := 0
	for pos < len(clean) {
		cr := strings.Index(up[pos:], "CREATE")
		if cr < 0 {
			break
		}
		cr += pos
		if cr != 0 && clean[cr-1] != '\n' && clean[cr-1] != '\r' {
			pos = cr + 6
			continue
		}
		if cr+6 < len(clean) && isIDChar(clean[cr+6]) {
			pos = cr + 6
			continue
		}
		eol := strings.IndexByte(clean[cr:], '\n')
		var lineEnd int
		if eol < 0 {
			lineEnd = len(clean)
		} else {
			lineEnd = cr + eol
		}
		headerLine := clean[cr:lineEnd]
		for i := 0; i < len(headerLine); i++ {
			if isIDStart(headerLine[i]) {
				st := i
				for i < len(headerLine) && isIDChar(headerLine[i]) {
					i++
				}
				id := strings.ToUpper(headerLine[st:i])
				if id != "CREATE" && id != "OR" && id != "REPLACE" {
					reserved[id] = struct{}{}
				}
			}
		}
		pos = lineEnd + 1
	}
}

// protectPackageSpec protects all identifiers before "PACKAGE BODY" (the public
// spec) when both a spec and a body are present.
func protectPackageSpec(clean string, reserved map[string]struct{}) {
	bodyPos := strings.Index(strings.ToUpper(clean), "PACKAGE BODY")
	if bodyPos < 0 {
		return
	}
	i := 0
	for i < bodyPos {
		switch {
		case clean[i] == '\'':
			i++
			for i < bodyPos {
				if clean[i] == '\'' {
					if i+1 < bodyPos && clean[i+1] == '\'' {
						i++
					} else {
						break
					}
				}
				i++
			}
			i++
		case clean[i] == '-' && i+1 < len(clean) && clean[i+1] == '-':
			i += 2
			for i < bodyPos && clean[i] != '\n' && clean[i] != '\r' {
				i++
			}
		case clean[i] == '/' && i+1 < len(clean) && clean[i+1] == '*':
			i += 2
			for i+1 < bodyPos && !(clean[i] == '*' && clean[i+1] == '/') {
				i++
			}
			if i+1 < bodyPos {
				i += 2
			} else {
				i++
			}
		case isIDStart(clean[i]):
			st := i
			for i < bodyPos && isIDChar(clean[i]) {
				i++
			}
			reserved[strings.ToUpper(clean[st:i])] = struct{}{}
		default:
			i++
		}
	}
}

// protectParameters protects PROCEDURE/FUNCTION parameter names.
func protectParameters(clean string, reserved map[string]struct{}) {
	up := strings.ToUpper(clean)
	for _, kw := range []string{"PROCEDURE", "FUNCTION"} {
		pos := 0
		for {
			p := strings.Index(up[pos:], kw)
			if p < 0 {
				break
			}
			p += pos
			paren := strings.IndexByte(up[p:], '(')
			if paren < 0 {
				pos = p + 1
				continue
			}
			paren += p
			depth := 1
			end := paren + 1
			for end < len(up) && depth > 0 {
				if up[end] == '(' {
					depth++
				} else if up[end] == ')' {
					depth--
				}
				end++
			}
			if depth != 0 {
				pos = p + 1
				continue
			}
			params := clean[paren+1 : end-1]
			collectParamNames(params, reserved)
			pos = end
		}
	}
}

func collectParamNames(params string, reserved map[string]struct{}) {
	pi := 0
	for pi < len(params) {
		for pi < len(params) && (params[pi] == ' ' || params[pi] == '\t' || params[pi] == '\n') {
			pi++
		}
		if pi >= len(params) {
			break
		}
		pk := strings.ToUpper(params[pi:])
		switch {
		case strings.HasPrefix(pk, "IN OUT"):
			pi += 6
			continue
		case strings.HasPrefix(pk, "IN"):
			pi += 2
			continue
		case strings.HasPrefix(pk, "OUT"):
			pi += 3
			continue
		case strings.HasPrefix(pk, "NOCOPY"):
			pi += 6
			continue
		}
		if !isIDStart(params[pi]) {
			pi++
			continue
		}
		ns := pi
		for pi < len(params) && isIDChar(params[pi]) {
			pi++
		}
		reserved[strings.ToUpper(params[ns:pi])] = struct{}{}
		for pi < len(params) && params[pi] != ',' && params[pi] != ')' {
			pi++
		}
	}
}

// ------------------------------------------------------------------
// Mapping (de)serialization
// ------------------------------------------------------------------

func mappingToString(m map[string]string) string {
	// Deterministic order for stable output.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(m[k])
		sb.WriteByte('|')
	}
	return sb.String()
}

func stringToMapping(s string) map[string]string {
	m := make(map[string]string)
	pos := 0
	for pos < len(s) {
		eq := strings.IndexByte(s[pos:], '=')
		if eq < 0 {
			break
		}
		eq += pos
		bar := strings.IndexByte(s[eq+1:], '|')
		if bar < 0 {
			break
		}
		bar += eq + 1
		m[s[pos:eq]] = s[eq+1 : bar]
		pos = bar + 1
	}
	return m
}

// ------------------------------------------------------------------
// Formatting transforms
// ------------------------------------------------------------------

// stripFormatting joins lines and collapses whitespace to make the obfuscated
// output maximally compact. -- comments are kept on their own line.
func stripFormatting(src string) string {
	var out strings.Builder
	needSpace := false
	atLineStart := true
	lastByte := func() byte {
		s := out.String()
		if len(s) == 0 {
			return 0
		}
		return s[len(s)-1]
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\'' {
			if needSpace && out.Len() > 0 && lastByte() != ' ' && lastByte() != '\n' {
				out.WriteByte(' ')
			}
			needSpace = false
			atLineStart = false
			out.WriteByte(c)
			i++
			for i < len(src) {
				out.WriteByte(src[i])
				if src[i] == '\'' {
					if i+1 < len(src) && src[i+1] == '\'' {
						i++
						out.WriteByte(src[i])
					} else {
						break
					}
				}
				i++
			}
			continue
		}
		if c == '-' && i+1 < len(src) && src[i+1] == '-' {
			if out.Len() > 0 && lastByte() != '\n' {
				out.WriteByte('\n')
			}
			out.WriteByte(c)
			out.WriteByte(src[i+1])
			i += 2
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				out.WriteByte(src[i])
				i++
			}
			out.WriteByte('\n')
			needSpace = false
			atLineStart = true
			continue
		}
		if c == '\n' || c == '\r' {
			needSpace = true
			atLineStart = true
			continue
		}
		if atLineStart && (c == ' ' || c == '\t') {
			continue
		}
		atLineStart = false
		if c == ' ' || c == '\t' {
			if out.Len() > 0 && lastByte() != ' ' && lastByte() != '\n' {
				out.WriteByte(' ')
			}
			continue
		}
		if needSpace && out.Len() > 0 && lastByte() != ' ' {
			out.WriteByte(' ')
		}
		needSpace = false
		out.WriteByte(c)
	}
	return out.String()
}

// reindentPLSQL applies minimal, safe indentation: it only adjusts depth for
// lines beginning with END (dedent) and lines beginning with BEGIN (indent).
func reindentPLSQL(src string) string {
	var out strings.Builder
	var line strings.Builder
	indent := 0

	flush := func() {
		l := strings.TrimRight(line.String(), " \t")
		line.Reset()
		if l == "" {
			return
		}
		trim := strings.ToUpper(strings.TrimLeft(l, " "))
		if strings.HasPrefix(trim, "END ") || trim == "END" || strings.HasPrefix(trim, "END;") {
			if indent > 0 {
				indent--
			}
		}
		n := indent
		if n > 10 {
			n = 10
		}
		out.WriteString(strings.Repeat("  ", n))
		out.WriteString(l)
		out.WriteByte('\n')
		if strings.HasPrefix(trim, "BEGIN") && !strings.Contains(trim, "END") {
			indent++
		}
	}

	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\'':
			line.WriteByte(c)
			i++
			for i < len(src) {
				line.WriteByte(src[i])
				if src[i] == '\'' {
					if i+1 < len(src) && src[i+1] == '\'' {
						i++
						line.WriteByte(src[i])
					} else {
						break
					}
				}
				i++
			}
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			flush()
			line.WriteByte(c)
			line.WriteByte(src[i+1])
			i += 2
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				line.WriteByte(src[i])
				i++
			}
			flush()
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			line.WriteString("/*")
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				line.WriteByte(src[i])
				i++
			}
			if i+1 < len(src) {
				line.WriteString("*/")
				i += 2
			}
		case c == '(':
			line.WriteByte(c)
			d := 1
			i++
			for i < len(src) && d > 0 {
				if src[i] == '(' {
					d++
				} else if src[i] == ')' {
					d--
				}
				if d > 0 {
					line.WriteByte(src[i])
					i++
				} else {
					break
				}
			}
			line.WriteByte(')')
		case c == ';':
			line.WriteByte(';')
			flush()
		case c == ' ' || c == '\t':
			s := line.String()
			if len(s) > 0 && s[len(s)-1] != ' ' {
				line.WriteByte(' ')
			}
		case c == '\n' || c == '\r':
			// drop raw newlines; flush happens on ; and comments
		default:
			line.WriteByte(c)
		}
	}
	flush()
	return out.String()
}
