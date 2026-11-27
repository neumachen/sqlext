package dbsql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// parameterPrefix marks the start of a named parameter.
	parameterPrefix = '@'
	// sqlPlaceholderPrefix is the prefix for positional parameter placeholders.
	// TODO: add support for other drivers, currently only supports PostgreSQL.
	sqlPlaceholderPrefix = "$"
)

var (
	// errUnterminatedConstruct is returned when a quoted string, quoted identifier,
	// block comment, or dollar-quoted string is still open at the end of the
	// statement. Rewriting past such a construct would treat literal text as SQL.
	errUnterminatedConstruct = errors.New("unterminated lexical construct")
	// errMixedPlaceholders is returned when a statement uses both named parameters
	// and native positional placeholders. Occurrence-based numbering would assign
	// numbers that collide with the native placeholders.
	errMixedPlaceholders = errors.New("named parameters cannot be mixed with positional placeholders")
	// errAmbiguousOperator is returned when a named parameter directly follows
	// operator characters that, together with its '@', spell a built-in
	// PostgreSQL operator. Both readings are valid SQL, so guessing would
	// silently change the meaning or the bound arguments of the statement.
	errAmbiguousOperator = errors.New("ambiguous operator before named parameter")
)

// PrepareStatement takes an unprepared SQL statement and returns a PreparedStatement interface.
// The PreparedStatement interface provides methods for managing named parameters, binding parameter
// values, and executing the prepared statement.
//
// The PrepareStatement function does the following:
//
//  1. Replaces named parameters (indicated by a '@' prefix) with positional placeholders ($1, $2, etc.).
//     Every occurrence receives its own placeholder, numbered in order of appearance; repeated names
//     are bound to all of their positions.
//  2. Stores the positions of the named parameters in a ParameterPositions struct.
//  3. Returns a preparedStatement struct that implements the PreparedStatement interface.
//
// A named parameter is '@' followed by one or more letters, digits, combining marks, or underscores.
// The rewrite follows PostgreSQL lexical rules so that parameter-like text is left untouched inside
// string constants (including E'...' escape strings), quoted identifiers, line comments, nested block
// comments, and tagged or untagged dollar-quoted strings. Standard-conforming strings are assumed,
// so backslashes only escape inside E'...' strings.
//
// PostgreSQL reads a run of operator characters (+ - * / < > = ~ ! @ # % ^ & | ` ?) as a single
// operator, so in x<@name it sees the <@ operator applied to the column name, while the named
// parameter syntax sees < applied to the parameter @name. When the operator characters directly
// before a parameter's '@' combine with it into one of the built-in operators <@, @@, @@@, or @-@,
// PrepareStatement returns an error instead of guessing. Use whitespace to state the intent:
//
//	x <@ name     -- the <@ operator and the column name; no parameter
//	x < @name     -- the < operator and the parameter @name
//	x @@ query    -- the @@ operator and the column query; no parameter
//	x @@ @query   -- the @@ operator and the parameter @query
//	x @@@ query   -- the @@@ operator and the column query; no parameter
//	x @@ @@query  -- rejected: @@ again directly precedes a parameter
//	@-@ path      -- the @-@ operator and the column path; no parameter
//	@ -@path      -- the @ operator applied to -@path, a negated parameter
//
// Other operators directly before a parameter keep their established meaning; x=@a, x>=@a,
// x||@a, x@>@a, and %@a% all bind @a. A lone '@' followed by a name is always a parameter, so the
// prefix absolute-value operator must be written with a space, as in @ col, or as abs(col).
//
// An error is returned, with no PreparedStatement, if a quoted string, quoted identifier, block
// comment, or dollar-quoted string is not terminated, if an operator before a named parameter is
// ambiguous as described above, or if named parameters are mixed with native positional
// placeholders such as $1. Statements that only use native placeholders are returned unchanged.
// PrepareStatement does not otherwise validate SQL.
//
// Example usage:
//
//	preparedStmt, err := PrepareStatement("SELECT * FROM users WHERE name = @name AND age > @age")
//	if err != nil {
//		// handle error
//	}
//
//	rows, err := preparedStmt.Query(db,
//		BindNamedParameterValue("name", "John"),
//		BindNamedParameterValue("age", 30),
//	)
//	if err != nil {
//		// handle error
//	}
//	defer rows.Close()
//
//	for rows.Next() {
//		// process rows
//	}
func PrepareStatement(unpreparedStatement string) (PreparedStatement, error) {
	r := statementRewriter{src: unpreparedStatement, nativePlaceholderAt: -1}
	if err := r.rewrite(); err != nil {
		return nil, fmt.Errorf("prepare statement: %w", err)
	}

	return &preparedStatement{
		originalStatement:     unpreparedStatement,
		namedParamPositions:   &r.positions,
		revisedStatement:      r.out.String(),
		boundNamedParamValues: make(BoundParameterValues, r.count),
	}, nil
}

// statementRewriter scans a statement once, byte by byte. Every character that
// carries lexical meaning in PostgreSQL is ASCII, and UTF-8 continuation bytes
// are never ASCII, so byte scanning cannot split or misread multi-byte text.
// Source text is copied to out in unmodified slices, which preserves it exactly.
type statementRewriter struct {
	src                 string
	out                 strings.Builder
	positions           ParameterPositions
	count               int
	copied              int
	nativePlaceholderAt int
	// operatorStart and operatorEnd delimit the most recent run of operator
	// characters. The run is adjacent to offset i only when operatorEnd == i;
	// comments, literals, and other tokens break adjacency, as they do for the
	// PostgreSQL lexer.
	operatorStart int
	operatorEnd   int
}

func (r *statementRewriter) rewrite() error {
	for i := 0; i < len(r.src); {
		next, err := r.step(i)
		if err != nil {
			return err
		}
		i = next
	}

	if r.count > 0 && r.nativePlaceholderAt >= 0 {
		return fmt.Errorf("%w: positional placeholder at byte %d", errMixedPlaceholders, r.nativePlaceholderAt)
	}

	r.out.WriteString(r.src[r.copied:])

	return nil
}

// step handles the token starting at i and returns the offset after it.
func (r *statementRewriter) step(i int) (int, error) {
	switch c := r.src[i]; {
	case c == '\'':
		return skipQuoted(r.src, i, '\'', r.isEscapeStringQuote(i))
	case c == '"':
		return skipQuoted(r.src, i, '"', false)
	case c == '-' && r.byteAt(i+1) == '-':
		return skipLineComment(r.src, i), nil
	case c == '/' && r.byteAt(i+1) == '*':
		return skipBlockComment(r.src, i)
	case c == '$' && !r.followsIdentifier(i):
		return r.dollar(i)
	case c == parameterPrefix:
		return r.namedParameter(i)
	default:
		if isOperatorByte(c) {
			r.extendOperatorRun(i)
		}
		return i + 1, nil
	}
}

func (r *statementRewriter) extendOperatorRun(i int) {
	if r.operatorEnd != i {
		r.operatorStart = i
	}
	r.operatorEnd = i + 1
}

// operatorRunBefore returns the operator characters that end directly at i.
func (r *statementRewriter) operatorRunBefore(i int) string {
	if r.operatorEnd != i {
		return ""
	}
	return r.src[r.operatorStart:i]
}

// dollar handles a '$' that starts a token: a native positional placeholder,
// a dollar-quoted string, or a lone '$' that is left for the server to judge.
func (r *statementRewriter) dollar(i int) (int, error) {
	if isASCIIDigit(r.byteAt(i + 1)) {
		if r.nativePlaceholderAt < 0 {
			r.nativePlaceholderAt = i
		}
		end := i + 1
		for isASCIIDigit(r.byteAt(end)) {
			end++
		}
		return end, nil
	}

	tag, ok := dollarQuoteTag(r.src, i)
	if !ok {
		return i + 1, nil
	}

	body := i + len(tag)
	closing := strings.Index(r.src[body:], tag)
	if closing < 0 {
		return 0, fmt.Errorf("%w: dollar-quoted string starting at byte %d", errUnterminatedConstruct, i)
	}

	return body + closing + len(tag), nil
}

// namedParameter replaces the parameter starting at i, if any, with the next
// positional placeholder. An '@' without a name is an operator character.
func (r *statementRewriter) namedParameter(i int) (int, error) {
	end := i + 1
	for end < len(r.src) {
		ch, size := utf8.DecodeRuneInString(r.src[end:])
		if !isParameterNameRune(ch) {
			break
		}
		end += size
	}

	if end == i+1 {
		r.extendOperatorRun(i)
		return end, nil
	}

	if run := r.operatorRunBefore(i); isBuiltinOperatorEndingInAt(run + "@") {
		return 0, fmt.Errorf(
			"%w: %q at byte %d reads as operator %q or as %q followed by parameter %q; separate them with whitespace",
			errAmbiguousOperator, r.src[r.operatorStart:end], r.operatorStart, run+"@", run, r.src[i:end],
		)
	}

	r.positions.insert(r.src[i+1:end], r.count)
	r.count++

	r.out.WriteString(r.src[r.copied:i])
	r.out.WriteString(sqlPlaceholderPrefix)
	r.out.WriteString(strconv.Itoa(r.count))
	r.copied = end

	return end, nil
}

// isEscapeStringQuote reports whether the quote at i opens an E'...' string, where
// backslash escapes apply. The E must start its own token; in abcE'x' it is part
// of an identifier and the quote opens a standard string.
func (r *statementRewriter) isEscapeStringQuote(i int) bool {
	prefix := r.byteAt(i - 1)
	return (prefix == 'E' || prefix == 'e') && !isIdentifierByte(r.byteAt(i-2))
}

// followsIdentifier reports whether the byte before i continues an identifier.
// PostgreSQL identifiers may contain '$', so in a$1 or a$$ the '$' starts neither
// a placeholder nor a dollar quote.
func (r *statementRewriter) followsIdentifier(i int) bool {
	return isIdentifierByte(r.byteAt(i - 1))
}

// byteAt returns 0 outside the statement so callers can look around i without
// bounds checks; 0 never matches any byte the rewriter treats as significant.
func (r *statementRewriter) byteAt(i int) byte {
	if i < 0 || i >= len(r.src) {
		return 0
	}
	return r.src[i]
}

// skipQuoted returns the offset after the quoted string or identifier that
// starts at start. A doubled quote is an escaped quote. When backslashEscapes is
// set, a backslash escapes the following byte.
func skipQuoted(src string, start int, quote byte, backslashEscapes bool) (int, error) {
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if backslashEscapes {
				i++
			}
		case quote:
			if i+1 < len(src) && src[i+1] == quote {
				i++
				continue
			}
			return i + 1, nil
		}
	}

	kind := "quoted string"
	if quote == '"' {
		kind = "quoted identifier"
	}

	return 0, fmt.Errorf("%w: %s starting at byte %d", errUnterminatedConstruct, kind, start)
}

// skipLineComment returns the offset of the line break ending the comment that
// starts at start. A comment running to the end of the statement is complete.
func skipLineComment(src string, start int) int {
	if end := strings.IndexAny(src[start:], "\r\n"); end >= 0 {
		return start + end
	}
	return len(src)
}

// skipBlockComment returns the offset after the block comment that starts at
// start. PostgreSQL block comments nest, so each /* needs its own */.
func skipBlockComment(src string, start int) (int, error) {
	depth := 1
	for i := start + 2; i+1 < len(src); {
		switch {
		case src[i] == '/' && src[i+1] == '*':
			depth++
			i += 2
		case src[i] == '*' && src[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i, nil
			}
		default:
			i++
		}
	}

	return 0, fmt.Errorf("%w: block comment starting at byte %d", errUnterminatedConstruct, start)
}

// dollarQuoteTag returns the opening delimiter ($$ or $tag$) of the dollar quote
// starting at start. Tags follow PostgreSQL rules: an identifier start byte
// followed by identifier bytes other than '$'.
func dollarQuoteTag(src string, start int) (string, bool) {
	for i := start + 1; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '$':
			return src[start : i+1], true
		case isDollarTagStartByte(c):
		case i > start+1 && isASCIIDigit(c):
		default:
			return "", false
		}
	}

	return "", false
}

// isBuiltinOperatorEndingInAt lists the built-in PostgreSQL operators whose
// last character is '@'. The prefix operator @ itself is not listed: a lone
// '@' before a name has always meant a named parameter.
func isBuiltinOperatorEndingInAt(op string) bool {
	switch op {
	case "<@", "@@", "@@@", "@-@":
		return true
	default:
		return false
	}
}

func isOperatorByte(c byte) bool {
	return strings.IndexByte("+-*/<>=~!@#%^&|`?", c) >= 0
}

func isParameterNameRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}

// isDollarTagStartByte treats every non-ASCII byte as a letter, as the
// PostgreSQL lexer does.
func isDollarTagStartByte(c byte) bool {
	return c == '_' || c >= utf8.RuneSelf || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isIdentifierByte(c byte) bool {
	return c == '$' || isASCIIDigit(c) || isDollarTagStartByte(c)
}

func isASCIIDigit(c byte) bool {
	return '0' <= c && c <= '9'
}
