package ucwrap

import "strings"

// renamableLocals returns the UPPER-cased names that can safely be renamed in
// src: names declared as private locals (collectRenamableLocals) minus every
// name that could be read in some other way if renamed (collectUnsafeNames).
func renamableLocals(src string) map[string]struct{} {
	locals := collectRenamableLocals(src)
	if len(locals) == 0 {
		return locals
	}
	for name := range collectUnsafeNames(tokenizePLSQL(src), locals) {
		delete(locals, name)
	}
	return locals
}

// sqlStartKeywords begin a static SQL statement (or a subquery) inside PL/SQL.
var sqlStartKeywords = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "WITH": true,
}

// collectUnsafeNames returns the subset of cand (UPPER-cased names declared as
// locals) that must NOT be renamed because the same spelling can also denote
// something else, where renaming only one side would change meaning or break
// compilation:
//
//   - a member after '.' (rec.status, pkg.g_cache), a bind (:name) or an inquiry
//     directive ($$name);
//   - a named-notation argument (name => value);
//   - a procedure/function/cursor parameter or a record field name;
//   - a quoted identifier ("NAME");
//   - any occurrence inside static SQL other than an INTO target. In SQL a column
//     named like a PL/SQL variable wins over the variable, so after renaming the
//     variable the statement would silently bind to the variable instead of the
//     column. Without the schema we cannot rule a clash out, so any local that
//     appears in static SQL is left alone.
//
// Declining is always the safe direction.
func collectUnsafeNames(toks []tkn, cand map[string]struct{}) map[string]struct{} {
	bad := make(map[string]struct{})
	mark := func(t tkn) {
		if t.kind != 'i' {
			return
		}
		if _, ok := cand[t.up]; ok {
			bad[t.up] = struct{}{}
		}
	}
	isSym := func(j int, text string) bool {
		return j >= 0 && j < len(toks) && toks[j].kind == 's' && toks[j].text == text
	}
	isKw := func(j int, kws ...string) bool {
		if j < 0 || j >= len(toks) || toks[j].kind != 'i' {
			return false
		}
		for _, k := range kws {
			if toks[j].up == k {
				return true
			}
		}
		return false
	}

	// Pass 1: syntactic contexts.
	const (
		ctxOther  = 0
		ctxParams = 1
		ctxRecord = 2
	)
	var stack []int
	itemStart := false
	for j, t := range toks {
		switch t.kind {
		case 'q':
			if strings.HasPrefix(t.text, `"`) {
				up := strings.ToUpper(strings.Trim(t.text, `"`))
				if _, ok := cand[up]; ok {
					bad[up] = struct{}{}
				}
			}
		case 's':
			switch t.text {
			case "(":
				ctx := ctxOther
				switch {
				case isKw(j-1, "RECORD"):
					ctx = ctxRecord
				case j >= 2 && toks[j-1].kind == 'i' && isKw(j-2, "PROCEDURE", "FUNCTION", "CURSOR"):
					ctx = ctxParams
				}
				stack = append(stack, ctx)
				itemStart = ctx != ctxOther
			case ")":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				itemStart = false
			case ",":
				itemStart = len(stack) > 0 && stack[len(stack)-1] != ctxOther
			default:
				itemStart = false
			}
		case 'i':
			if isSym(j-1, ".") && !isSym(j-2, ".") {
				mark(t) // member access
			}
			if isSym(j-1, ":") || isSym(j-1, "$") {
				mark(t) // bind variable / inquiry directive
			}
			if isSym(j+1, "=") && isSym(j+2, ">") {
				mark(t) // named-notation argument
			}
			if itemStart {
				mark(t) // parameter or record field name
			}
			itemStart = false
		default:
			itemStart = false
		}
	}

	// Pass 2: static SQL regions.
	depth := 0
	inSQL := false
	sqlDepth := 0
	sqlKind := ""
	sawReturning := false
	inInto := false
	for j, t := range toks {
		if t.kind == 's' {
			switch t.text {
			case "(":
				depth++
			case ")":
				if depth > 0 {
					depth--
				}
				if inSQL && depth < sqlDepth {
					inSQL, inInto = false, false
				}
			case ";":
				if inSQL && depth <= sqlDepth {
					inSQL, inInto = false, false
				}
			}
			continue
		}
		if t.kind != 'i' {
			continue
		}
		if !inSQL {
			if sqlStartKeywords[t.up] && !isSym(j-1, ".") {
				inSQL, sqlDepth, sqlKind = true, depth, t.up
				sawReturning, inInto = false, false
			}
			continue
		}
		switch t.up {
		case "RETURNING":
			sawReturning = true
		case "INTO":
			target := false
			switch sqlKind {
			case "INSERT":
				target = sawReturning
			case "MERGE":
				target = false
			default:
				target = true
			}
			inInto = target
			continue
		case "FROM", "LIMIT":
			inInto = false
		}
		if !inInto {
			mark(t)
		}
	}
	return bad
}
