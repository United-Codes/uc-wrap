package plsqlwrap

import "strings"

// isV2At reports whether the WRAPPED keyword at wrappedPos is followed (after
// whitespace) by the V2 marker "a000000".
func isV2At(s string, wrappedPos int) bool {
	rest := s[wrappedPos+7:]
	i := 0
	for i < len(rest) && isSpace(rest[i]) {
		i++
	}
	return i+7 <= len(rest) && rest[i:i+7] == "a000000"
}

// findNextWrapped returns the index of the next V2 "WRAPPED" marker at or after
// pos, or -1 if none. (V1 markers are ignored — V1 is not supported.)
func findNextWrapped(s string, pos int) int {
	up := strings.ToUpper(s)
	for pos < len(s) {
		p := strings.Index(up[pos:], "WRAPPED")
		if p < 0 {
			return -1
		}
		p += pos
		if isV2At(s, p) {
			return p
		}
		pos = p + 1
	}
	return -1
}

// findUnitStart walks back up to four lines from a WRAPPED marker to find the
// "CREATE ..." line that begins the wrapped unit.
func findUnitStart(s string, wrappedPos int) int {
	searchStart := wrappedPos
	for i := 0; i < 4; i++ {
		if searchStart == 0 {
			break
		}
		prevNL := strings.LastIndexByte(s[:searchStart], '\n')
		var lineStart int
		if prevNL < 0 {
			lineStart = 0
		} else {
			lineStart = prevNL + 1
		}
		line := strings.TrimLeft(s[lineStart:searchStart], " \t")
		if strings.HasPrefix(strings.ToUpper(line), "CREATE") {
			return lineStart
		}
		if lineStart == 0 {
			break
		}
		searchStart = lineStart
	}
	nl := strings.LastIndexByte(s[:wrappedPos+1], '\n')
	if nl < 0 {
		return 0
	}
	return nl + 1
}

// findUnitEnd returns the end of a wrapped unit: the next line-starting "CREATE"
// after unitStart, or end of string.
func findUnitEnd(s string, unitStart int) int {
	up := strings.ToUpper(s)
	searchFrom := unitStart + 1
	for searchFrom < len(s) {
		p := strings.Index(up[searchFrom:], "CREATE")
		if p < 0 {
			return len(s)
		}
		p += searchFrom
		if p == 0 || s[p-1] == '\n' {
			return p
		}
		searchFrom = p + 1
	}
	return len(s)
}

// UnwrapAll unwraps every V2 wrapped section found in source, passing through
// any non-wrapped text (DDL/DML between units) unchanged. Files containing a
// single unit, multiple units, or a mix of wrapped and plain statements are all
// handled.
func UnwrapAll(source string) string {
	var out strings.Builder
	cursor := 0
	for {
		wpos := findNextWrapped(source, cursor)
		if wpos < 0 {
			out.WriteString(source[cursor:])
			break
		}
		unitStart := findUnitStart(source, wpos)
		unitEnd := findUnitEnd(source, unitStart)
		out.WriteString(source[cursor:unitStart])
		unwrapped, _ := UnwrapV2(source[unitStart:unitEnd])
		out.WriteString(unwrapped)
		cursor = unitEnd
	}
	return out.String()
}

// ApplyCompat strips a "schema." prefix from CREATE object names so that Oracle
// does not truncate the name at the dot when wrapping. It returns the modified
// source.
func ApplyCompat(input string) string {
	result := input
	up := strings.ToUpper(result)

	pos := 0
	for pos < len(result) {
		cr := strings.Index(up[pos:], "CREATE")
		if cr < 0 {
			break
		}
		cr += pos
		// Must be at the start of a line (or of the source).
		if cr > 0 && result[cr-1] != '\n' {
			pos = cr + 6
			continue
		}

		afterCR := indexWithin(up, "FUNCTION ", cr, 30)
		if afterCR < 0 {
			afterCR = indexWithin(up, "PROCEDURE ", cr, 30)
		}
		if afterCR < 0 {
			afterCR = indexWithin(up, "PACKAGE BODY ", cr, 30)
		}
		if afterCR < 0 {
			afterCR = indexWithin(up, "PACKAGE ", cr, 30)
		}

		if afterCR >= 0 {
			kwEnd := afterCR
			for kwEnd < len(up) && up[kwEnd] != ' ' {
				kwEnd++
			}
			namePos := kwEnd + 1
			if namePos < len(result) {
				dot := strings.IndexByte(result[namePos:], '.')
				if dot >= 0 {
					dot += namePos
					nameEnd := strings.IndexAny(result[namePos:], " (\n\r")
					if nameEnd < 0 {
						nameEnd = len(result)
					} else {
						nameEnd += namePos
					}
					if dot < nameEnd {
						result = result[:namePos] + result[dot+1:]
						up = strings.ToUpper(result)
					}
				}
			}
		}
		pos = cr + 1
	}
	return result
}

// indexWithin returns the index of sub at or after from, but only if it occurs
// within `window` bytes of from; otherwise -1.
func indexWithin(s, sub string, from, window int) int {
	if from >= len(s) {
		return -1
	}
	p := strings.Index(s[from:], sub)
	if p < 0 || p > window {
		return -1
	}
	return from + p
}
