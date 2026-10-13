package plsqlwrap

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// WrapV2 encodes plain PL/SQL source into Oracle's V2 wrapped format.
//
// When keepComments is false (the default behavior of Oracle's wrap), comments
// are stripped first. When preserveCase is false the body is uppercased
// (Oracle preserves only the object name's case); set preserveCase to true to
// keep the source verbatim, as the obfuscator does.
//
// The output is a valid wrapped unit that Oracle can install. It is NOT
// byte-identical to Oracle's own wrap because Go's deflate differs from zlib's,
// but the embedded SHA-1 is computed over our own compressed bytes, so Oracle
// accepts and decompresses it correctly.
func WrapV2(source string, keepComments, preserveCase bool) string {
	// 1. Optionally strip comments.
	cleaned := source
	if !keepComments {
		cleaned = stripComments(source)
	}

	// 2. Extract the CREATE header for the preamble.
	header := extractHeader(cleaned)
	if header == "" {
		header = "CREATE OR REPLACE"
	}

	// Strip the "CREATE OR REPLACE " prefix before compressing; the unwrapper
	// adds it back.
	compressSource := cleaned
	if cr := strings.Index(strings.ToUpper(compressSource), "CREATE OR REPLACE "); cr >= 0 {
		compressSource = compressSource[cr+18:]
	}

	// Oracle strips everything after the last ';' then null-terminates.
	if semi := strings.LastIndexByte(compressSource, ';'); semi >= 0 {
		compressSource = compressSource[:semi+1]
	}
	compressSource += "\x00"

	// 3. Uppercase (unless preserveCase), keeping string literals intact, then
	// restore the original case of the object name.
	normalized := compressSource
	if !preserveCase {
		normalized = upperPreservingStrings(compressSource)
		normalized = restoreObjectNameCase(normalized, header)
	}

	// 4-8. zlib deflate, SHA-1 digest, inverse substitution cipher, base64.
	// A first base64 line starting with '/' would be mistaken for a statement
	// terminator by SQL script splitters (and splitBase64 cannot shorten the
	// first line), so fall back to a lower compression level, which changes
	// the digest and thus the first character, until it does not start with '/'.
	var b64 string
	for level := zlib.BestCompression; level >= zlib.BestSpeed; level-- {
		compressed := zlibDeflate(normalized, level)
		if len(compressed) == 0 {
			return source
		}
		digest := sha1.Sum(compressed)
		payload := make([]byte, 0, 20+len(compressed))
		payload = append(payload, digest[:]...)
		payload = append(payload, compressed...)
		for i, b := range payload {
			payload[i] = cipherTo[b]
		}
		b64 = base64.StdEncoding.EncodeToString(payload)
		if !strings.HasPrefix(b64, "/") {
			break
		}
	}

	// 9. Build the wrapped output (Oracle 19c+ format).
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(" wrapped \n")
	sb.WriteString("a000000\n")
	sb.WriteString("1\n")
	for i := 0; i < 15; i++ {
		sb.WriteString("abcd\n")
	}
	sb.WriteString(objectTypeCode(header))
	sb.WriteByte('\n')

	// Terminator: <uncompressed length> <lcode length>, both hex.
	//
	// The second field is the length of the base64 block as written to the
	// stream — the base64 characters plus the internal newlines that separate
	// the 72-char lines (the trailing newline after the last line is not
	// counted). That equals len(b64) + numLines - 1.
	//
	// The upstream C++ tool used len(b64)+1, which only matches when the
	// payload fits in <= 2 lines; Oracle 23ai rejects a wrong value with
	// PLS-00753 ("malformed or corrupted wrapped unit") for larger units.
	lines := splitBase64(b64, 72)
	fmt.Fprintf(&sb, "%x %x\n", len(normalized), len(b64)+len(lines)-1)

	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n/\n")
	return sb.String()
}

// splitBase64 breaks b64 into lines of (at most) width characters. A line that
// would start with '/' is avoided by ending the previous line one character
// early: SQL script splitters (e.g. APEX SQL Workshop) may treat a line that
// begins with '/' as a statement terminator and cut the wrapped unit in half.
func splitBase64(b64 string, width int) []string {
	var lines []string
	for i := 0; i < len(b64); {
		end := i + width
		if end >= len(b64) {
			end = len(b64)
		} else {
			for end > i+1 && b64[end] == '/' {
				end--
			}
		}
		lines = append(lines, b64[i:end])
		i = end
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

var objectTypeRe = regexp.MustCompile(`(?i)\b(package\s+body|type\s+body|package|function|procedure|type)\b`)

// objectTypeCode returns the single-character object type code Oracle writes
// after the "abcd" filler lines of a wrapped unit (as produced by
// DBMS_DDL.WRAP): 9 package, b package body, 8 function, 7 procedure, d type,
// e type body. Unknown headers fall back to 8.
func objectTypeCode(header string) string {
	m := objectTypeRe.FindString(header)
	switch strings.Join(strings.Fields(strings.ToLower(m)), " ") {
	case "package":
		return "9"
	case "package body":
		return "b"
	case "function":
		return "8"
	case "procedure":
		return "7"
	case "type":
		return "d"
	case "type body":
		return "e"
	}
	return "8"
}

// stripComments removes -- single-line and /* */ multi-line comments, leaving
// the contents of '...' string literals (including ” escapes) untouched.
// Newlines that followed a -- comment are preserved.
func stripComments(s string) string {
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
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				i++
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			} else {
				i++
			}
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

// extractHeader returns the "CREATE ... <object name>" prefix that Oracle puts
// before the WRAPPED keyword — everything up to (but excluding) the first '(',
// newline, or a RETURN/IS/AS keyword that follows the object name.
func extractHeader(source string) string {
	upper := strings.ToUpper(source)
	p := strings.Index(upper, "CREATE")
	if p < 0 {
		return ""
	}

	var result strings.Builder
	inQuotes := false
	for i := p; i < len(source); i++ {
		c := source[i]
		if c == '"' {
			inQuotes = !inQuotes
			result.WriteByte(c)
			continue
		}
		if !inQuotes {
			if c == '(' || c == '\n' || c == '\r' {
				break
			}
			if i > p && keywordAt(source, i, "RETURN") {
				break
			}
			if i > p && keywordAt(source, i, "IS") {
				break
			}
			if i > p && keywordAt(source, i, "AS") {
				break
			}
		}
		result.WriteByte(c)
	}
	return strings.TrimRight(result.String(), " \t")
}

// keywordAt reports whether the keyword kw (case-insensitive) begins at index i
// in s and is followed by a space, newline, or end of string.
func keywordAt(s string, i int, kw string) bool {
	if i+len(kw) > len(s) {
		return false
	}
	if !strings.EqualFold(s[i:i+len(kw)], kw) {
		return false
	}
	after := i + len(kw)
	return after >= len(s) || s[after] == ' ' || s[after] == '\n'
}

// upperPreservingStrings uppercases everything except the contents of '...'
// string literals. A single quote toggles literal mode (mirroring the
// reference implementation, which does not special-case ” escapes here).
func upperPreservingStrings(s string) string {
	b := []byte(s)
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '\'' {
			inString = !inString
			continue
		}
		if !inString {
			b[i] = toUpperByte(b[i])
		}
	}
	return string(b)
}

// restoreObjectNameCase re-applies the original case of the object name (as it
// appears in header) to the uppercased normalized body, since Oracle preserves
// the object name's case.
func restoreObjectNameCase(normalized, header string) string {
	if len(header) <= 18 {
		return normalized
	}
	up := strings.ToUpper(header)

	var nameStart int = -1
	switch {
	case strings.Contains(up, "FUNCTION"):
		nameStart = strings.Index(up, "FUNCTION") + 9
	case strings.Contains(up, "PROCEDURE"):
		nameStart = strings.Index(up, "PROCEDURE") + 10
	case strings.Contains(up, "PACKAGE BODY"):
		nameStart = strings.Index(up, "PACKAGE BODY") + 13
	case strings.Contains(up, "PACKAGE"):
		nameStart = strings.Index(up, "PACKAGE") + 8
	}
	if nameStart < 0 || nameStart >= len(header) {
		return normalized
	}

	objName := strings.TrimRight(header[nameStart:], " ")
	if objName == "" {
		return normalized
	}
	upName := strings.ToUpper(objName)

	var kw string
	switch {
	case strings.Contains(up, "FUNCTION"):
		kw = "FUNCTION "
	case strings.Contains(up, "PROCEDURE"):
		kw = "PROCEDURE "
	case strings.Contains(up, "PACKAGE BODY"):
		kw = "PACKAGE BODY "
	case strings.Contains(up, "PACKAGE"):
		kw = "PACKAGE "
	}
	if kw == "" {
		return normalized
	}

	pos := strings.Index(normalized, kw+upName)
	if pos < 0 {
		return normalized
	}
	pos += len(kw)
	b := []byte(normalized)
	for k := 0; k < len(objName) && pos+k < len(b); k++ {
		b[pos+k] = objName[k]
	}
	return string(b)
}

func zlibDeflate(in string, level int) []byte {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		return nil
	}
	if _, err := w.Write([]byte(in)); err != nil {
		w.Close()
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func toUpperByte(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}
