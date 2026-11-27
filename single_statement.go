package sqlext

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMultipleStatements is returned by Exec, Query, QueryRow and their Context
// variants when the SQL they would send contains more than one statement. The
// statement is rejected before anything is sent to the database.
var ErrMultipleStatements = errors.New("sqlext: multiple SQL statements are not allowed")

var (
	// errUnicodeStringNeedsConformingStrings mirrors the PostgreSQL lexer, which
	// refuses U&'...' strings while standard_conforming_strings is off.
	errUnicodeStringNeedsConformingStrings = errors.New("string constant with Unicode escapes (U&'...') requires standard_conforming_strings")
	// errAmbiguousContinuation is returned when string constants are joined
	// across a line break by whitespace that includes a vertical tab, which
	// PostgreSQL versions do not treat consistently.
	errAmbiguousContinuation = errors.New("string constant continuation with a vertical tab is ambiguous")
)

// checkSingleStatement reports an error unless sql contains at most one
// statement, whatever the server's standard_conforming_strings setting is.
//
// The execution helpers send SQL without an explicit Prepare, and some drivers
// (lib/pq among them) then run parameterless SQL through the simple query
// protocol, which executes every statement in the string. The server-side
// "one command per prepared statement" guard therefore no longer applies and
// is enforced here instead.
//
// The scan follows the PostgreSQL lexer: semicolons inside string constants,
// quoted identifiers, line comments, nested block comments and dollar-quoted
// strings do not separate statements, and empty statements (a trailing
// semicolon, or one followed only by whitespace and comments) are ignored.
//
// Whether a backslash escapes a quote in a plain '...' string depends on
// standard_conforming_strings, so the same text can be one statement with the
// setting on and several with it off. The SQL is therefore read both ways, and
// it is rejected when either reading lexes completely and finds more than one
// statement. A reading that stops at a lexical error, such as an unterminated
// string, means the server would reject the whole text under that setting
// without running any of it, so only the other reading decides. When neither
// reading lexes, the error is returned.
//
// SQL-standard routine bodies (CREATE FUNCTION ... BEGIN ATOMIC ...; END) are a
// single statement that contains semicolons. Telling those apart from
// statement separators needs the grammar, not the lexer, so they are rejected;
// a dollar-quoted body or database/sql directly can be used instead.
func checkSingleStatement(sql string) error {
	var lexErr error
	lexed := false
	for _, conforming := range [...]bool{true, false} {
		secondAt, err := scanStatements(sql, conforming)
		switch {
		case errors.Is(err, errAmbiguousContinuation):
			return fmt.Errorf("check statement: %w", err)
		case err != nil:
			if lexErr == nil {
				lexErr = err
			}
		case secondAt >= 0:
			return fmt.Errorf("%w: another statement starts at byte %d when standard_conforming_strings is %s",
				ErrMultipleStatements, secondAt, onOff(conforming))
		default:
			lexed = true
		}
	}
	if !lexed {
		return fmt.Errorf("check statement: %w", lexErr)
	}
	return nil
}

// scanStatements reads sql as the PostgreSQL lexer does with the given
// standard_conforming_strings setting. It returns the offset where a second
// non-empty statement starts, or -1, and any lexical error. It reads to the
// end even after finding a second statement, because a later lexical error
// stops the server from running any statement.
func scanStatements(sql string, conforming bool) (int, error) {
	secondAt, statements, inStatement := -1, 0, false
	for i := 0; i < len(sql); {
		c := sql[i]
		var (
			next = i + 1
			err  error
		)
		switch {
		case c == ';':
			inStatement = false
		case isSQLSpace(c):
		case c == '-' && byteAt(sql, i+1) == '-':
			next = skipLineComment(sql, i)
		case c == '/' && byteAt(sql, i+1) == '*':
			next, err = skipBlockComment(sql, i)
		default:
			if !inStatement {
				inStatement = true
				statements++
				if statements == 2 {
					secondAt = i
				}
			}
			next, err = skipStatementToken(sql, i, conforming)
		}
		if err != nil {
			return -1, err
		}
		i = next
	}
	return secondAt, nil
}

// skipStatementToken returns the offset after the token at i when that token
// can contain a semicolon (a string constant, quoted identifier or
// dollar-quoted string), and i+1 for any other byte. Identifiers, numbers,
// operators and placeholders never contain semicolons, so they can be walked
// byte by byte.
func skipStatementToken(sql string, i int, conforming bool) (int, error) {
	switch c := sql[i]; {
	case c == '\'':
		kind, err := stringConstantKindAt(sql, i, conforming)
		if err != nil {
			return 0, err
		}
		return skipStringConstant(sql, i, kind)
	case c == '"':
		return skipQuoted(sql, i, '"', false)
	case c == '$' && !followsIdentifier(sql, i):
		if isASCIIDigit(byteAt(sql, i+1)) {
			return i + 1, nil
		}
		tag, ok := dollarQuoteTag(sql, i)
		if !ok {
			return i + 1, nil
		}
		body := i + len(tag)
		closing := strings.Index(sql[body:], tag)
		if closing < 0 {
			return 0, fmt.Errorf("%w: dollar-quoted string starting at byte %d", errUnterminatedConstruct, i)
		}
		return body + closing + len(tag), nil
	default:
		return i + 1, nil
	}
}

// stringConstantKind is how the PostgreSQL lexer reads the body of a string
// constant.
type stringConstantKind int

const (
	// stringStandard: '' is a quote and backslash is an ordinary character.
	stringStandard stringConstantKind = iota
	// stringEscape: '' is a quote and backslash escapes the next character.
	stringEscape
	// stringBits: B'...' and X'...' end at the first quote.
	stringBits
)

// stringConstantKindAt classifies the string constant whose opening quote is
// at i. A prefix letter only counts when it starts its own token. N'...'
// strings are plain strings preceded by the NCHAR keyword.
func stringConstantKindAt(sql string, i int, conforming bool) (stringConstantKind, error) {
	prefix := byteAt(sql, i-1)
	prefixStartsToken := !isIdentifierByte(byteAt(sql, i-2))
	switch {
	case (prefix == 'E' || prefix == 'e') && prefixStartsToken:
		return stringEscape, nil
	case (prefix == 'B' || prefix == 'b' || prefix == 'X' || prefix == 'x') && prefixStartsToken:
		return stringBits, nil
	case prefix == '&' && (byteAt(sql, i-2) == 'U' || byteAt(sql, i-2) == 'u') && !isIdentifierByte(byteAt(sql, i-3)):
		if !conforming {
			return 0, fmt.Errorf("%w: at byte %d", errUnicodeStringNeedsConformingStrings, i-2)
		}
		return stringStandard, nil
	case conforming:
		return stringStandard, nil
	default:
		return stringEscape, nil
	}
}

// skipStringConstant returns the offset after the string constant that starts
// at start. Constants separated only by whitespace that includes a line break
// are one constant read in the same way, as in the PostgreSQL lexer, so an
// E'...' constant continued on the next line keeps its backslash escapes.
func skipStringConstant(sql string, start int, kind stringConstantKind) (int, error) {
	for i := start + 1; i < len(sql); i++ {
		switch sql[i] {
		case '\\':
			if kind == stringEscape {
				i++
			}
		case '\'':
			if kind != stringBits && byteAt(sql, i+1) == '\'' {
				i++
				continue
			}
			quote, ok, err := continuationQuote(sql, i+1)
			if err != nil {
				return 0, err
			}
			if !ok {
				return i + 1, nil
			}
			i = quote
		}
	}
	return 0, fmt.Errorf("%w: string constant starting at byte %d", errUnterminatedConstruct, start)
}

// continuationQuote reports whether the text from i is whitespace containing a
// line break, optionally with line comments, followed by a quote that
// continues the current string constant, and returns that quote's offset.
// Block comments and other tokens end the constant.
func continuationQuote(sql string, i int) (quote int, ok bool, err error) {
	sawNewline, sawVerticalTab := false, false
	for i < len(sql) {
		switch c := sql[i]; {
		case c == ' ' || c == '\t' || c == '\f':
			i++
		case c == '\n' || c == '\r':
			sawNewline = true
			i++
		case c == '\v':
			sawVerticalTab = true
			i++
		case c == '-' && byteAt(sql, i+1) == '-':
			i = skipLineComment(sql, i)
		case c == '\'' && sawNewline:
			if sawVerticalTab {
				return 0, false, fmt.Errorf("%w: at byte %d", errAmbiguousContinuation, i)
			}
			return i, true, nil
		default:
			return 0, false, nil
		}
	}
	return 0, false, nil
}

// Values of the standard_conforming_strings setting, used in error messages.
const (
	settingOn  = "on"
	settingOff = "off"
)

func onOff(on bool) string {
	if on {
		return settingOn
	}
	return settingOff
}

// isSQLSpace matches whitespace between tokens. PostgreSQL 16 rejects a
// vertical tab outright; counting it as whitespace here is safe because it can
// only make the server reject the text.
func isSQLSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}
