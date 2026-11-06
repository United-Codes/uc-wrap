package ucwrap

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// ErrNotWrapped is returned by UnwrapV2 when the input does not contain a
// recognizable V2 wrapped section.
var ErrNotWrapped = errors.New("ucwrap: input is not V2-wrapped (no \"wrapped a000000\" marker)")

// ErrCorruptData is returned when the wrapped payload cannot be decoded or
// decompressed (truncated base64, bad zlib stream, etc.).
var ErrCorruptData = errors.New("ucwrap: corrupt or invalid V2 wrapped data")

// isHexLine reports whether line is the V2 terminator: two space-separated
// hexadecimal tokens (e.g. "2b 60"). Trailing CR/space/tab are ignored.
func isHexLine(line string) bool {
	line = strings.TrimRight(line, "\r \t")
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return false
	}
	first, second := line[:sp], line[sp+1:]
	if first == "" || second == "" {
		return false
	}
	return isHex(first) && isHex(second)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// UnwrapV2 decodes a single V2-wrapped PL/SQL unit and returns the recovered
// source (prefixed with "CREATE OR REPLACE " and terminated with "/\n").
//
// If source does not contain a V2 wrapped section it returns the input
// unchanged together with ErrNotWrapped, mirroring the lenient behavior of the
// reference implementation while still signaling the condition to callers that
// care.
func UnwrapV2(source string) (string, error) {
	// 1. Find the "wrapped" marker (case-insensitive), then "a000000".
	upper := strings.ToUpper(source)
	wpos := strings.Index(upper, "WRAPPED")
	if wpos < 0 {
		return source, ErrNotWrapped
	}

	p := wpos + 7
	for p < len(source) && isSpace(source[p]) {
		p++
	}
	if p+7 > len(source) || strings.ToUpper(source[p:p+7]) != "A000000" {
		return source, ErrNotWrapped
	}

	// 2. Scan forward for the terminator line ("<hex> <hex>").
	p += 7
	termPos := -1
	for p < len(source) {
		nl := strings.IndexByte(source[p:], '\n')
		var lineEnd int
		if nl < 0 {
			lineEnd = len(source)
		} else {
			lineEnd = p + nl
		}
		line := strings.TrimRight(source[p:lineEnd], "\r \t")
		if isHexLine(line) {
			termPos = p
			break
		}
		if lineEnd >= len(source) {
			break
		}
		p = lineEnd + 1
	}
	if termPos < 0 {
		return source, ErrNotWrapped
	}

	// 3. Base64 data starts on the line after the terminator.
	nl := strings.IndexByte(source[termPos:], '\n')
	if nl < 0 {
		return source, ErrNotWrapped
	}
	dataStart := termPos + nl + 1
	b64 := source[dataStart:]

	// 4. Base64 decode (whitespace stripped).
	decoded, err := decodeBase64Lenient(b64)
	if err != nil || len(decoded) < 21 {
		return source, ErrCorruptData
	}

	// 5. Reverse the substitution cipher.
	for i, b := range decoded {
		decoded[i] = cipherFrom[b]
	}

	// 6. Strip the 20-byte SHA-1 digest, keep the compressed payload.
	compressed := decoded[20:]

	// 7. zlib inflate.
	uncompressed, err := zlibInflate(compressed)
	if err != nil {
		return source, ErrCorruptData
	}

	// Oracle appends a trailing NUL byte (sometimes several) — drop them.
	uncompressed = bytes.TrimRight(uncompressed, "\x00")

	// 8. Reassemble.
	var sb strings.Builder
	sb.WriteString("CREATE OR REPLACE ")
	sb.Write(uncompressed)
	out := sb.String()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out += "\n"
	}
	out += "/\n"
	return out, nil
}

// decodeBase64Lenient strips all ASCII whitespace then base64-decodes, skipping
// any stray non-alphabet characters and stopping at the first '=' padding —
// matching the tolerant decoder in the reference implementation.
func decodeBase64Lenient(in string) ([]byte, error) {
	var cleaned strings.Builder
	cleaned.Grow(len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			continue
		}
		cleaned.WriteByte(c)
	}
	s := cleaned.String()
	// StdEncoding (with padding) handles well-formed Oracle output. Fall back
	// to a manual streaming decode for anything it rejects.
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	return manualBase64Decode(s), nil
}

// manualBase64Decode mirrors the C++ decoder: skip unknown chars, stop at '='.
func manualBase64Decode(s string) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var dec [256]int
	for i := range dec {
		dec[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		dec[alphabet[i]] = i
	}
	out := make([]byte, 0, len(s)/4*3+3)
	val, bits := 0, -8
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			break
		}
		d := dec[c]
		if d < 0 {
			continue
		}
		val = (val << 6) | d
		bits += 6
		if bits >= 0 {
			out = append(out, byte((val>>bits)&0xFF))
			bits -= 8
		}
	}
	return out
}

func zlibInflate(in []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
