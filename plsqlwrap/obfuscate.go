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

	// Per-call protected set, seeded with the global reserved words.
	reserved := make(map[string]struct{}, len(reservedWords))
	for w := range reservedWords {
		reserved[w] = struct{}{}
	}

	protectHeaderIdentifiers(clean, reserved)
	protectPackageSpec(clean, reserved)
	protectParameters(clean, reserved)

	freq := collectIdentifiers(clean, reserved)
	allIDs := collectAllIdentifiers(clean)
	mapping := makeMapping(freq, allIDs, reserved)

	obfuscated := clean
	if len(mapping) > 0 {
		obfuscated = replaceIdentifiers(clean, mapping)
	}
	obfuscated = stripFormatting(obfuscated)

	// Reverse map (short -> original), including an uppercase alias so
	// deobfuscation works whether or not the wrapper uppercased the source.
	reverse := make(map[string]string, len(mapping)*2)
	for orig, short := range mapping {
		reverse[short] = orig
		up := strings.ToUpper(short)
		if up != short {
			reverse[up] = orig
		}
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

// skipToNextID advances past string literals, comments and quoted identifiers,
// returning the index of the next identifier-start character (or len(s)).
func skipToNextID(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == '\'':
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
		case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				i++
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i += 2
		case s[i] == '"':
			i++
			for i < len(s) && s[i] != '"' {
				i++
			}
			i++
		case !isIDStart(s[i]):
			i++
		default:
			return i
		}
	}
	return i
}

// collectIdentifiers returns identifier -> frequency, skipping reserved words,
// names of length <= 2, and digit-leading tokens.
func collectIdentifiers(s string, reserved map[string]struct{}) map[string]int {
	freq := make(map[string]int)
	i := 0
	for i < len(s) {
		i = skipToNextID(s, i)
		if i >= len(s) || !isIDStart(s[i]) {
			break
		}
		start := i
		for i < len(s) && isIDChar(s[i]) {
			i++
		}
		word := s[start:i]
		up := strings.ToUpper(word)
		if _, ok := reserved[up]; ok {
			continue
		}
		if len(word) <= 2 {
			continue
		}
		if isDigit(word[0]) {
			continue
		}
		freq[word]++
	}
	return freq
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

// replaceIdentifiers rewrites identifiers per mapping while copying strings,
// comments, quoted identifiers and dotted references (pkg.func) verbatim.
func replaceIdentifiers(s string, mapping map[string]string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\'':
			out.WriteByte(s[i])
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
		case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
			out.WriteByte(s[i])
			out.WriteByte(s[i+1])
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				out.WriteByte(s[i])
				i++
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			out.WriteByte(s[i])
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
		case s[i] == '"':
			out.WriteByte(s[i])
			i++
			for i < len(s) && s[i] != '"' {
				out.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				out.WriteByte(s[i])
				i++
			}
		case isIDStart(s[i]):
			start := i
			for i < len(s) && isIDChar(s[i]) {
				i++
			}
			word := s[start:i]

			// Look ahead for a dot => dotted reference, keep the chain intact.
			peek := i
			for peek < len(s) && (s[peek] == ' ' || s[peek] == '\t') {
				peek++
			}
			if peek < len(s) && s[peek] == '.' {
				out.WriteString(word)
				for i < len(s) {
					if s[i] == '.' {
						out.WriteByte(s[i])
						i++
						continue
					}
					if isIDStart(s[i]) {
						for i < len(s) && isIDChar(s[i]) {
							out.WriteByte(s[i])
							i++
						}
						p := i
						for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
							p++
						}
						if !(p < len(s) && s[p] == '.') {
							break
						}
					} else {
						break
					}
				}
			} else if repl, ok := mapping[word]; ok {
				out.WriteString(repl)
			} else {
				out.WriteString(word)
			}
		default:
			out.WriteByte(s[i])
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
