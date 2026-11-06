package ucwrap

// lexemeEnd reports whether a string literal, quoted identifier or comment
// starts at s[i]. It returns the index just past that lexeme and its kind:
//
//	's' string literal ('...' with ” escapes, or q'[...]' / nq'{...}' alternative quoting)
//	'd' quoted identifier ("...")
//	'c' comment (-- to end of line, or /* ... */)
//	0   nothing starts here (end == i)
//
// It is the single place that knows PL/SQL's lexical quirks, so every scanner
// in the package (comment stripping, upper-casing, identifier renaming, ...)
// agrees on what is code and what is literal text. Callers must only invoke it
// at lexeme boundaries (never in the middle of an identifier) and consume
// identifiers whole, because a q/nq prefix is only meaningful at the start of
// a word.
func lexemeEnd(s string, i int) (end int, kind byte) {
	if i >= len(s) {
		return i, 0
	}
	switch c := s[i]; {
	case c == '\'':
		j := i + 1
		for j < len(s) {
			if s[j] == '\'' {
				if j+1 < len(s) && s[j+1] == '\'' {
					j += 2
					continue
				}
				return j + 1, 's'
			}
			j++
		}
		return len(s), 's'
	case c == '"':
		j := i + 1
		for j < len(s) && s[j] != '"' {
			j++
		}
		if j < len(s) {
			j++
		}
		return j, 'd'
	case c == '-' && i+1 < len(s) && s[i+1] == '-':
		j := i + 2
		for j < len(s) && s[j] != '\n' && s[j] != '\r' {
			j++
		}
		return j, 'c'
	case c == '/' && i+1 < len(s) && s[i+1] == '*':
		j := i + 2
		for j+1 < len(s) && !(s[j] == '*' && s[j+1] == '/') {
			j++
		}
		if j+1 < len(s) {
			return j + 2, 'c'
		}
		return len(s), 'c'
	case c == 'q' || c == 'Q' || c == 'n' || c == 'N':
		j := i
		if c == 'n' || c == 'N' {
			if j+1 < len(s) && (s[j+1] == 'q' || s[j+1] == 'Q') {
				j++
			} else {
				return i, 0
			}
		}
		// s[j] is the q; an alternative-quoted literal is q'<delim>...<delim>'.
		if j+2 < len(s) && s[j+1] == '\'' {
			d := s[j+2]
			if d == ' ' || d == '\t' || d == '\n' || d == '\r' {
				return i, 0
			}
			closer := d
			switch d {
			case '[':
				closer = ']'
			case '(':
				closer = ')'
			case '{':
				closer = '}'
			case '<':
				closer = '>'
			}
			for k := j + 3; k+1 < len(s); k++ {
				if s[k] == closer && s[k+1] == '\'' {
					return k + 2, 's'
				}
			}
			return len(s), 's'
		}
	}
	return i, 0
}

// numberEnd returns the index just past the numeric literal starting at s[i]
// (a digit). It does not swallow a '..' range operator or identifier text that
// follows, so `1..coll.count` tokenizes as 1 .. coll . count.
func numberEnd(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i+1 < len(s) && s[i] == '.' && isDigit(s[i+1]) {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	if i < len(s) && (s[i] == 'd' || s[i] == 'D' || s[i] == 'f' || s[i] == 'F') && (i+1 >= len(s) || !isIDChar(s[i+1])) {
		i++
	}
	return i
}
