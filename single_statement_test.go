package dbsql

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	twoStatements = "SELECT 1; SELECT 2"
	// separatorHiddenWhenOff is two statements when standard_conforming_strings
	// is on, because the backslash does not escape the quote.
	separatorHiddenWhenOff = `SELECT 'a\'; DROP TABLE t; --'`
)

func TestCheckSingleStatementAccepts(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "plain", sql: "SELECT 1"},
		{name: "empty", sql: ""},
		{name: "only whitespace and comments", sql: " \n-- note\n/* block */ "},
		{name: "trailing semicolon", sql: "SELECT 1;"},
		{name: "several trailing semicolons", sql: "SELECT 1;;\n;"},
		{name: "leading empty statement", sql: "; SELECT 1"},
		{name: "trailing whitespace", sql: "SELECT 1;\n\t\r\f\v "},
		{name: "trailing line comment", sql: "SELECT 1; -- done; SELECT 2"},
		{name: "trailing block comment", sql: "SELECT 1; /* SELECT 2; */"},
		{name: "trailing nested block comment", sql: "SELECT 1; /* a /* SELECT 2; */ still comment; */"},
		{name: "comment before semicolon", sql: "SELECT 1 /* ; */ ;"},
		{name: "semicolon in string", sql: "SELECT ';'"},
		{name: "doubled quote in string", sql: "SELECT 'it''s; fine'"},
		{name: "injection-shaped string", sql: "SELECT 'a''; DROP TABLE t; --'"},
		{name: "escape string with escaped quote", sql: `SELECT E'a\'; DROP TABLE t; --'`},
		{name: "lowercase escape string", sql: `SELECT e'\\'';'`},
		{name: "unicode escape string", sql: `SELECT U&'d\0061t;a'`},
		{name: "bit string", sql: "SELECT B'01'; "},
		{name: "semicolon in quoted identifier", sql: `SELECT 1 AS "a;b"`},
		{name: "doubled quote in identifier", sql: `SELECT 1 AS "a"";b"`},
		{name: "semicolon in line comment", sql: "SELECT 1 -- ; SELECT 2\n"},
		{name: "semicolon in block comment", sql: "SELECT /* ; SELECT 2 */ 1"},
		{name: "untagged dollar quote", sql: "SELECT $$;$$"},
		{name: "tagged dollar quote body", sql: "SELECT $tag$ ; $$ ; $tag$"},
		{name: "dollar quote holding quotes", sql: "SELECT $q$ '; DROP TABLE t; -- $q$;"},
		{
			name: "dollar-quoted function body",
			sql:  "CREATE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $body$ BEGIN RETURN 1; END; $body$;",
		},
		{name: "positional placeholders", sql: "SELECT $1, $2;"},
		{name: "identifier with dollar", sql: "SELECT a$b FROM t;"},
		{name: "non-ascii in string", sql: "SELECT 'ü;ö'"},
		{name: "operators and casts", sql: "SELECT x::int <@ y, a || b FROM t WHERE c >= $1;"},
		{name: "minus is not a comment", sql: "SELECT 1 - -1;"},
		{name: "slash is not a comment", sql: "SELECT 4 / 2;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, checkSingleStatement(tt.sql))
		})
	}
}

func TestCheckSingleStatementRejectsMultipleStatements(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "two statements", sql: twoStatements},
		{name: "no space", sql: "SELECT 1;SELECT 2"},
		{name: "empty statement between", sql: "SELECT 1; ; SELECT 2"},
		{name: "after string with semicolon", sql: "SELECT ';'; SELECT 2"},
		{name: "after line comment", sql: "SELECT 1; -- note\nSELECT 2"},
		{name: "line comment ended by carriage return", sql: "SELECT 1; -- note\rSELECT 2"},
		{name: "after block comment", sql: "SELECT 1 /* a */; SELECT 2"},
		{name: "after nested block comment", sql: "SELECT 1; /* a /* b */ c */ SELECT 2"},
		{name: "after dollar quote", sql: "SELECT $$x$$; DROP TABLE t"},
		{name: "after tagged dollar quote", sql: "SELECT $a$ ; $b$ ; $a$; DROP TABLE t"},
		{name: "after quoted identifier", sql: `SELECT "x;"; DROP TABLE t`},
		{name: "backslash does not escape in standard string", sql: separatorHiddenWhenOff},
		{name: "E inside identifier is not an escape prefix", sql: `SELECT xE'a\'; DROP TABLE t; --'`},
		{name: "identifier dollar is not a dollar quote", sql: "SELECT a$$; DROP TABLE t; --$$"},
		{name: "dollar after placeholder digits is not a dollar quote", sql: "SELECT $1$$; DROP TABLE t; --$$"},
		{name: "second statement is a comment-wrapped token", sql: "SELECT 1; /**/x"},
		{name: "non-ascii after semicolon", sql: "SELECT 1; ü"},
		{name: "unknown byte after semicolon", sql: "SELECT 1;\x00"},
		{name: "with parameters", sql: "INSERT INTO t VALUES ($1); INSERT INTO t VALUES ($2)"},
		{
			name: "SQL-standard function body is rejected",
			sql:  "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT 2; END",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSingleStatement(tt.sql)
			assert.ErrorIs(t, err, ErrMultipleStatements)
		})
	}
}

func TestCheckSingleStatementRejectsUnterminated(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "string", sql: "SELECT 'abc; SELECT 2"},
		{name: "escape string", sql: `SELECT E'abc\'; SELECT 2`},
		{name: "unterminated quoted identifier", sql: `SELECT "abc; SELECT 2`},
		{name: "block comment", sql: "SELECT 1 /* ; SELECT 2"},
		{name: "nested block comment", sql: "SELECT 1 /* /* */ ; SELECT 2"},
		{name: "dollar quote", sql: "SELECT $$; SELECT 2"},
		{name: "unterminated tagged dollar quote", sql: "SELECT $a$ ; $b$; SELECT 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSingleStatement(tt.sql)
			require.ErrorIs(t, err, errUnterminatedConstruct)
			assert.NotErrorIs(t, err, ErrMultipleStatements)
		})
	}
}

// The same text can be one statement with standard_conforming_strings on and
// several with it off, because only then does a backslash escape a quote in a
// plain string. The check must hold for both settings.
func TestCheckSingleStatementIndependentOfStringSetting(t *testing.T) {
	const separatorHiddenWhenOn = `SELECT 'a\' || '; DROP TABLE t; --'`

	rejected := []struct {
		name    string
		sql     string
		setting string
	}{
		{name: "separator hidden only when on", sql: separatorHiddenWhenOn, setting: settingOff},
		{name: "national string", sql: `SELECT N'a\' || '; DROP TABLE t; --'`, setting: settingOff},
		{
			name:    "insert hidden only when on",
			sql:     `INSERT INTO t (n, tag) VALUES (1, 'a\' || '); INSERT INTO t (n, tag) VALUES (2, $$hidden$$); --')`,
			setting: settingOff,
		},
		{name: "continued plain string", sql: "SELECT 'x'\n'\\' || '; DROP TABLE t; --'", setting: settingOff},
		{name: "separator hidden only when off", sql: separatorHiddenWhenOff, setting: settingOn},
		{name: "bit string continuation keeps backslash literal", sql: "SELECT B'1'\n'\\' ; DROP TABLE t; --'", setting: settingOn},
		{name: "unicode string has no backslash escape", sql: `SELECT U&'a\' ; DROP TABLE t; --'`, setting: settingOn},
	}
	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			err := checkSingleStatement(tt.sql)
			require.ErrorIs(t, err, ErrMultipleStatements)
			assert.ErrorContains(t, err, "standard_conforming_strings is "+tt.setting)
		})
	}

	accepted := []struct {
		name string
		sql  string
	}{
		{name: "escape string", sql: `SELECT E'it\'s; fine'`},
		{name: "escape string hiding text", sql: `SELECT E'x\' ); DROP TABLE t; --'`},
		{name: "escape string continuation keeps escapes", sql: "SELECT E'a'\n'\\' ; DROP TABLE t; --'"},
		{name: "escape string continuation across a comment", sql: "SELECT E'a' -- note\n'\\' ; DROP TABLE t; --'"},
		{name: "trailing backslash only valid when on", sql: `SELECT 'C:\temp\'`},
		{name: "backslash escape meaning differs but no separator", sql: `SELECT 'a\b; c'`},
		{name: "unterminated when off", sql: `SELECT 'a\', 'b;c'`},
		{name: "unterminated when on", sql: `SELECT 'a\'b'`},
		{name: "doubled quotes without backslash", sql: "SELECT 'it''s; fine', ';'"},
		{name: "bit string does not double quotes", sql: "SELECT B'0''; DROP TABLE t; --'"},
		{name: "unicode string valid only when on", sql: `SELECT U&'d\0061t;'`},
	}
	for _, tt := range accepted {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			assert.NoError(t, checkSingleStatement(tt.sql))
		})
	}

	t.Run("rejects text no setting can lex", func(t *testing.T) {
		require.ErrorIs(t, checkSingleStatement("SELECT U&'x"), errUnterminatedConstruct)
	})

	t.Run("rejects ambiguous continuation", func(t *testing.T) {
		err := checkSingleStatement("SELECT 'a'\v\n'b'")
		require.ErrorIs(t, err, errAmbiguousContinuation)
		assert.NotErrorIs(t, err, ErrMultipleStatements)
	})
}

func TestCheckSingleStatementReportsOffset(t *testing.T) {
	err := checkSingleStatement(twoStatements)
	assert.ErrorContains(t, err, "byte 10")
}

func FuzzCheckSingleStatement(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1", "SELECT 1; SELECT 2", "SELECT ';'", `SELECT E'\';'`, "SELECT $a$;$a$",
		"/* /* */ */;", "-- x\n;", `"a;b"`, "$1$$;$$", "SELECT 'x",
		`SELECT 'a\' || '; SELECT 2; --'`, "SELECT E'a'\n'\\' ; SELECT 2; --'", "SELECT B'1'\n'\\';", `SELECT U&'a\';`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, sql string) {
		err := checkSingleStatement(sql)
		if err != nil && !errors.Is(err, ErrMultipleStatements) && !errors.Is(err, errUnterminatedConstruct) &&
			!errors.Is(err, errAmbiguousContinuation) {
			t.Fatalf("unexpected error kind for %q: %v", sql, err)
		}
		if err != nil {
			return
		}
		// Appending semicolons, whitespace or a comment never adds a statement.
		for _, suffix := range []string{";", "\n;;", "\n-- tail", "\n/* tail */;"} {
			if err := checkSingleStatement(sql + suffix); err != nil {
				t.Fatalf("suffix %q made %q fail: %v", suffix, sql, err)
			}
		}
		// A statement after a terminated one is always rejected.
		if err := checkSingleStatement("SELECT 1;\n" + sql + "\n;SELECT 2"); !errors.Is(err, ErrMultipleStatements) {
			t.Fatalf("second statement after %q was not rejected: %v", sql, err)
		}
	})
}
