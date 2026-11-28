package sqlext

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Statements, names and values repeated across the table-driven tests.
const (
	positionalCol1Col2 = "SELECT * FROM table WHERE col1 = $1 AND col2 = $2"
	positionalCol1     = "SELECT * FROM table WHERE col1 = $1"
	namedCol1          = "SELECT * FROM table WHERE col1 = @foo"
	textSomething      = "something"
	textElse           = "else"
	textName           = "name"
	textFoo            = "foo"
	textBar            = "bar"
)

type PrepareStatementTest struct {
	Name                       string
	UnpreparedStatement        string
	ExpectedStatement          string
	ExpectedParameterPositions *ParameterPositions
}

func TestPrepareStatement(t *testing.T) {
	tests := []PrepareStatementTest{
		{
			UnpreparedStatement:        "SELECT * FROM table WHERE col1 = 1",
			ExpectedStatement:          "SELECT * FROM table WHERE col1 = 1",
			ExpectedParameterPositions: nil,
			Name:                       "No Parameter",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @name",
			ExpectedStatement:   positionalCol1,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					textName: {0},
				},
				totalPositions: 1,
			},
			Name: "Single Parameter",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @name AND col2 = @occupation",
			ExpectedStatement:   positionalCol1Col2,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					textName:     {0},
					"occupation": {1},
				},
				totalPositions: 2,
			},
			Name: "Two Parameters",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @name AND col2 = @name",
			ExpectedStatement:   positionalCol1Col2,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					textName: {0, 1},
				},
				totalPositions: 2,
			},
			Name: "Repeated Named Parameter",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 IN (@something, @else)",
			ExpectedStatement:   "SELECT * FROM table WHERE col1 IN ($1, $2)",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					textSomething: {0},
					textElse:      {1},
				},
				totalPositions: 2,
			},
			Name: "Parameters In Parenthesis",
		},
		{
			UnpreparedStatement:        "SELECT * FROM table WHERE col1 = '@literal' AND col2 LIKE '@literal'",
			ExpectedStatement:          "SELECT * FROM table WHERE col1 = '@literal' AND col2 LIKE '@literal'",
			ExpectedParameterPositions: nil,
			Name:                       "Escaped Parameters",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = '@literal' AND col2 = @literal AND col3 LIKE '@literal'",
			ExpectedStatement:   "SELECT * FROM table WHERE col1 = '@literal' AND col2 = $1 AND col3 LIKE '@literal'",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"literal": {0},
				},
				totalPositions: 1,
			},
			Name: "Escaping and non-escaping parameters",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @foo AND col2 IN (SELECT id FROM tabl2 WHERE col10 = @bar)",
			ExpectedStatement:   "SELECT * FROM table WHERE col1 = $1 AND col2 IN (SELECT id FROM tabl2 WHERE col10 = $2)",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					textFoo: {0},
					textBar: {1},
				},
				totalPositions: 2,
			},
			Name: "Parameters In Subclause",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @1234567890 AND col2 = @0987654321",
			ExpectedStatement:   positionalCol1Col2,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"1234567890": {0},
					"0987654321": {1},
				},
				totalPositions: 2,
			},
			Name: "Numeric Parameters",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @ABCDEFGHIJKLMNOPQRSTUVWXYZ",
			ExpectedStatement:   positionalCol1,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"ABCDEFGHIJKLMNOPQRSTUVWXYZ": {0},
				},
				totalPositions: 1,
			},
			Name: "Upcased Parameter",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @abc123ABC098",
			ExpectedStatement:   positionalCol1,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"abc123ABC098": {0},
				},
				totalPositions: 1,
			},
			Name: "Multicased alphanumeric parameter",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 LIKE %@t%",
			ExpectedStatement:   "SELECT * FROM table WHERE col1 LIKE %$1%",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"t": {0},
				},
				totalPositions: 1,
			},
			Name: "Pattern Matching",
		},
		{
			UnpreparedStatement: "ST_GeomFromText('POINT(' || @long @lat || ',4326)'",
			ExpectedStatement:   "ST_GeomFromText('POINT(' || $1 $2 || ',4326)'",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"long": {0},
					"lat":  {1},
				},
				totalPositions: 2,
			},
			Name: "Concated parameters",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @first_name AND col2 = @last_name",
			ExpectedStatement:   positionalCol1Col2,
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"first_name": {0},
					"last_name":  {1},
				},
				totalPositions: 2,
			},
			Name: "Snake Case",
		},
		{
			UnpreparedStatement: "SELECT * FROM table WHERE col1 @> @jsonParam1 AND col2 <@ @jsonParam2",
			ExpectedStatement:   "SELECT * FROM table WHERE col1 @> $1 AND col2 <@ $2",
			ExpectedParameterPositions: &ParameterPositions{
				parameterPositions: map[string][]int{
					"jsonParam1": {0},
					"jsonParam2": {1},
				},
				totalPositions: 2,
			},
			Name: "JSONB Operators",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			preparedStatement, err := PrepareStatement(test.UnpreparedStatement)
			require.NoError(t, err)
			require.Equal(t, test.ExpectedStatement, preparedStatement.Revised())
			actualParamPositions := preparedStatement.ParameterPositions()
			require.Equal(t, test.ExpectedParameterPositions, actualParamPositions, test.Name)
		})
	}
}

type BoundNamedParameterValuesTest struct {
	Name                string
	UnpreparedStatement string
	BindParameterValues []TestNamedParameterValue
	ExpectedBoundValues []any
}

type TestNamedParameterValue struct {
	Name  string
	Value any
}

func TestBindNamedParameterValues(t *testing.T) {
	tests := []BoundNamedParameterValuesTest{
		{
			Name:                "Single String Parameter",
			UnpreparedStatement: namedCol1,
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: textBar,
				},
			},
			ExpectedBoundValues: []any{
				textBar,
			},
		},
		{
			Name:                "Two String Parameter",
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @foo AND col2 = @foo2",
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: textBar,
				},
				{
					Name:  "foo2",
					Value: "bart",
				},
			},
			ExpectedBoundValues: []any{
				textBar,
				"bart",
			},
		},
		{
			Name:                "Repeated Parameters",
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @foo AND col2 = @foo",
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: textBar,
				},
			},
			ExpectedBoundValues: []any{
				textBar,
				textBar,
			},
		},
		{
			Name:                "Type Parameters",
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @str AND col2 = @int AND col3 = @pi",
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  "str",
					Value: textFoo,
				},
				{
					Name:  "int",
					Value: 1,
				},
				{
					Name:  "pi",
					Value: 3.14,
				},
			},
			ExpectedBoundValues: []any{
				textFoo,
				1,
				3.14,
			},
		},
		{
			Name:                "Ordered Parameters",
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @foo AND col2 = @bar AND col3 = @foo AND col4 = @foo AND col5 = @bar",
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: textSomething,
				},
				{
					Name:  textBar,
					Value: textElse,
				},
			},
			ExpectedBoundValues: []any{
				textSomething, textElse, textSomething, textSomething, textElse,
			},
		},
		{
			Name:                "Case Sensitive",
			UnpreparedStatement: "SELECT * FROM table WHERE col1 = @foo AND col2 = @FOO",
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: "baz",
				},
				{
					Name:  "FOO",
					Value: "quux",
				},
			},
			ExpectedBoundValues: []any{
				"baz", "quux",
			},
		},
		{
			Name:                "Nil Parameter",
			UnpreparedStatement: namedCol1,
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: pq.Array([]string{}),
				},
			},
			ExpectedBoundValues: []any{
				pq.Array([]string{}),
			},
		},
		{
			Name:                "Casted Type Parameter",
			UnpreparedStatement: namedCol1,
			BindParameterValues: []TestNamedParameterValue{
				{
					Name:  textFoo,
					Value: "'testing'::varchar",
				},
			},
			ExpectedBoundValues: []any{
				"'testing'::varchar",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			parameterFuncs := make([]BindParameterValueFunc, 0)
			prepraredStatement, err := PrepareStatement(test.UnpreparedStatement)
			require.NoError(t, err)

			for _, bindValue := range test.BindParameterValues {
				err = prepraredStatement.BindParameterValue(bindValue.Name, bindValue.Value)
				require.NoError(t, err)
				parameterFuncs = append(parameterFuncs, BindParameterValue(bindValue.Name, bindValue.Value))
			}

			err = prepraredStatement.BindParameterValues(parameterFuncs...)
			require.NoError(t, err)

			for posIndex, boundValue := range prepraredStatement.BoundParameterValues() {
				require.Equal(t, boundValue, test.ExpectedBoundValues[posIndex], test.Name)
			}
		})
	}
}

type parameterPositionsWant map[string][]int

func (w parameterPositionsWant) toParameterPositions() *ParameterPositions {
	if len(w) == 0 {
		return nil
	}
	total := 0
	for _, positions := range w {
		total += len(positions)
	}
	return &ParameterPositions{parameterPositions: w, totalPositions: total}
}

func TestPrepareStatementLexicalContexts(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		positions parameterPositionsWant
	}{
		{
			name:      "utf8 text and names",
			input:     "SELECT 'héllo wörld', \"naïve\", @名前 AS 日本, @café",
			want:      "SELECT 'héllo wörld', \"naïve\", $1 AS 日本, $2",
			positions: parameterPositionsWant{"名前": {0}, "café": {1}},
		},
		{
			name:      "combining mark in name",
			input:     "SELECT @cafe\u0301 = 'é'",
			want:      "SELECT $1 = 'é'",
			positions: parameterPositionsWant{"cafe\u0301": {0}},
		},
		{
			name:      "invalid utf8 preserved",
			input:     "SELECT '\xff\xfe', @a\xff",
			want:      "SELECT '\xff\xfe', $1\xff",
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "line comments",
			input:     "SELECT @a -- @b\r\n, @c --@d",
			want:      "SELECT $1 -- @b\r\n, $2 --@d",
			positions: parameterPositionsWant{"a": {0}, "c": {1}},
		},
		{
			name:      "nested block comments",
			input:     "SELECT /* outer /* inner @x */ still @y */ @z/**/@w",
			want:      "SELECT /* outer /* inner @x */ still @y */ $1/**/$2",
			positions: parameterPositionsWant{"z": {0}, "w": {1}},
		},
		{
			name:      "quoted identifiers",
			input:     `SELECT "col@x", "a""@b", @c FROM "t--"`,
			want:      `SELECT "col@x", "a""@b", $1 FROM "t--"`,
			positions: parameterPositionsWant{"c": {0}},
		},
		{
			name:      "doubled quote in string",
			input:     "SELECT 'it''s @x /* -- $$', @a",
			want:      "SELECT 'it''s @x /* -- $$', $1",
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "escape strings",
			input:     `SELECT E'\'@x', e'\\' || @a, E'a\\\'@y'`,
			want:      `SELECT E'\'@x', e'\\' || $1, E'a\\\'@y'`,
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "backslash is literal in standard strings",
			input:     `SELECT 'C:\' || @a`,
			want:      `SELECT 'C:\' || $1`,
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "trailing identifier E is not an escape prefix",
			input:     `SELECT fooE'\' || @a`,
			want:      `SELECT fooE'\' || $1`,
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "prefixed strings",
			input:     `SELECT U&'d\0061t @x', U&"@y", B'01', X'@z', N'@n', @a`,
			want:      `SELECT U&'d\0061t @x', U&"@y", B'01', X'@z', N'@n', $1`,
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "dollar quoted strings",
			input:     "SELECT $$ @x '$$, $fn$ '@y' $$ @z $fn$, $a1$ @w $a$ $a1$, $tâg$ @v $tâg$, @b",
			want:      "SELECT $$ @x '$$, $fn$ '@y' $$ @z $fn$, $a1$ @w $a$ $a1$, $tâg$ @v $tâg$, $1",
			positions: parameterPositionsWant{"b": {0}},
		},
		{
			name:      "dollar inside identifiers",
			input:     "SELECT a$1, b$$, c$tag$ FROM t WHERE x = @x",
			want:      "SELECT a$1, b$$, c$tag$ FROM t WHERE x = $1",
			positions: parameterPositionsWant{"x": {0}},
		},
		{
			name:      "lone dollar",
			input:     "SELECT $ @a, $-",
			want:      "SELECT $ $1, $-",
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "at operators",
			input:     "SELECT @ @a, x @> @b, x <@ @c, d @@ @q, d @@ to_tsquery(@r), @-@ s, x@>y, @@@",
			want:      "SELECT @ $1, x @> $2, x <@ $3, d @@ $4, d @@ to_tsquery($5), @-@ s, x@>y, @@@",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "q": {3}, "r": {4}},
		},
		{
			name:  "ambiguous operators written as operators",
			input: "SELECT x <@ name, @-@ path, d @@ query, d @@@ query, x<@ y",
			want:  "SELECT x <@ name, @-@ path, d @@ query, d @@@ query, x<@ y",
		},
		{
			name:      "ambiguous operators written as parameters",
			input:     "SELECT x < @limit, x< @a, @ -@path, d @@ @query, @ @abs, d @@@ @q, x <@ @c",
			want:      "SELECT x < $1, x< $2, @ -$3, d @@ $4, @ $5, d @@@ $6, x <@ $7",
			positions: parameterPositionsWant{"limit": {0}, "a": {1}, "path": {2}, "query": {3}, "abs": {4}, "q": {5}, "c": {6}},
		},
		{
			name:      "operators adjacent to parameters",
			input:     "SELECT x=@a, y||@b, z<>@c, -@d, @e+1, @f%2",
			want:      "SELECT x=$1, y||$2, z<>$3, -$4, $5+1, $6%2",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "d": {3}, "e": {4}, "f": {5}},
		},
		{
			name: "non-builtin operator runs before parameters",
			input: "SELECT x<=@a, x>=@b, x<<@c, x>>@d, x->@e, x->>@f, x#>@g, x@>@h, x@?@i, x<@@j, " +
				"x@@@@k, x*@l, x/@m, x+@n, x~@o, x!@p, x?@q, x&@r, x|@s, x^@t, x#@u, x`@v, x<@-@w, x--<@c\n",
			want: "SELECT x<=$1, x>=$2, x<<$3, x>>$4, x->$5, x->>$6, x#>$7, x@>$8, x@?$9, x<@$10, " +
				"x@@@$11, x*$12, x/$13, x+$14, x~$15, x!$16, x?$17, x&$18, x|$19, x^$20, x#$21, x`$22, x<@-$23, x--<@c\n",
			positions: parameterPositionsWant{
				"a": {0}, "b": {1}, "c": {2}, "d": {3}, "e": {4}, "f": {5}, "g": {6}, "h": {7}, "i": {8}, "j": {9},
				"k": {10}, "l": {11}, "m": {12}, "n": {13}, "o": {14}, "p": {15}, "q": {16}, "r": {17}, "s": {18},
				"t": {19}, "u": {20}, "v": {21}, "w": {22},
			},
		},
		{
			name:      "comments and literals break operator runs",
			input:     "SELECT x</**/@a, x<'s'@b, x<\"c\"@c, x<$$d$$@d, x<--z\n@e, x@-/**/@f",
			want:      "SELECT x</**/$1, x<'s'$2, x<\"c\"$3, x<$$d$$$4, x<--z\n$5, x@-/**/$6",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "d": {3}, "e": {4}, "f": {5}},
		},
		{
			name:      "casts",
			input:     "SELECT @a::int, @b::text[], CAST(@c AS jsonb), 'x'::text, @d::\"MyType\"",
			want:      "SELECT $1::int, $2::text[], CAST($3 AS jsonb), 'x'::text, $4::\"MyType\"",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "d": {3}},
		},
		{
			name:      "punctuation and end of input",
			input:     "INSERT INTO t VALUES (@a,@b);SELECT ARRAY[@c],@d.e,@f",
			want:      "INSERT INTO t VALUES ($1,$2);SELECT ARRAY[$3],$4.e,$5",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "d": {3}, "f": {4}},
		},
		{
			name:      "parameter followed by comment and quote",
			input:     "SELECT @a--c\n, @b/*c*/, @c'x', @d\"y\"",
			want:      "SELECT $1--c\n, $2/*c*/, $3'x', $4\"y\"",
			positions: parameterPositionsWant{"a": {0}, "b": {1}, "c": {2}, "d": {3}},
		},
		{
			name:      "numeric and underscore names",
			input:     "SELECT @1, @0987, @_a, @a_, @__, @a1_b2",
			want:      "SELECT $1, $2, $3, $4, $5, $6",
			positions: parameterPositionsWant{"1": {0}, "0987": {1}, "_a": {2}, "a_": {3}, "__": {4}, "a1_b2": {5}},
		},
		{
			name:  "lone at signs",
			input: "SELECT @, @ , 1 @",
			want:  "SELECT @, @ , 1 @",
		},
		{
			name:  "empty statement",
			input: "",
			want:  "",
		},
		{
			name:  "only native placeholders",
			input: "SELECT $1, $2 FROM t WHERE x = '@y' AND z = $10",
			want:  "SELECT $1, $2 FROM t WHERE x = '@y' AND z = $10",
		},
		{
			name:      "native placeholder text outside SQL tokens",
			input:     "SELECT '$1', \"$2\", $$ $3 $$ /* $4 */ -- $5\n, @a",
			want:      "SELECT '$1', \"$2\", $$ $3 $$ /* $4 */ -- $5\n, $1",
			positions: parameterPositionsWant{"a": {0}},
		},
		{
			name:      "repeated names are numbered per occurrence",
			input:     "SELECT @a, '@a', @b, @a",
			want:      "SELECT $1, '@a', $2, $3",
			positions: parameterPositionsWant{"a": {0, 2}, "b": {1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := runBounded(t, tt.input)
			require.NoError(t, err)
			require.NotNil(t, stmt)
			assert.Equal(t, tt.input, stmt.UnpreparedStatement())
			assert.Equal(t, tt.want, stmt.Revised())
			assert.Equal(t, tt.positions.toParameterPositions(), stmt.ParameterPositions())
		})
	}
}

func TestPrepareStatementBindsByPosition(t *testing.T) {
	stmt, err := runBounded(t, "SELECT @名前, '@名前', @b /* @b */, @名前, @c::int, @b")
	require.NoError(t, err)
	require.Equal(t, "SELECT $1, '@名前', $2 /* @b */, $3, $4::int, $5", stmt.Revised())

	err = stmt.BindParameterValues(
		BindParameterValue("名前", "ü"),
		BindParameterValue("b", 2),
		BindParameterValue("c", nil),
	)
	require.NoError(t, err)
	assert.Equal(t, BoundParameterValues{"ü", 2, "ü", nil, 2}, stmt.BoundParameterValues())

	stmt.ResetParametersValues()
	assert.Equal(t, BoundParameterValues{nil, nil, nil, nil, nil}, stmt.BoundParameterValues())
}

func TestPrepareStatementOnlyNativePlaceholders(t *testing.T) {
	stmt, err := runBounded(t, "SELECT $1 WHERE x = $2")
	require.NoError(t, err)
	assert.Equal(t, "SELECT $1 WHERE x = $2", stmt.Revised())
	assert.Nil(t, stmt.ParameterPositions())
	assert.Nil(t, stmt.BoundParameterValues())
}

// runBounded fails the test instead of hanging when PrepareStatement does not
// return; the previous implementation looped forever on an unterminated quote.
// On timeout the goroutine is abandoned, which is acceptable for a failing test.
func runBounded(t *testing.T, input string) (PreparedStatement, error) {
	t.Helper()

	type result struct {
		stmt PreparedStatement
		err  error
	}
	done := make(chan result, 1)
	go func() {
		stmt, err := PrepareStatement(input)
		done <- result{stmt: stmt, err: err}
	}()

	select {
	case res := <-done:
		return res.stmt, res.err
	case <-time.After(5 * time.Second):
		t.Fatalf("PrepareStatement did not return for %q", input)
		return nil, nil
	}
}

func TestPrepareStatementRejectsUnterminated(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "string", input: "SELECT 'abc, @a"},
		{name: "string ending in doubled quote", input: "SELECT 'it''"},
		{name: "lone quote at end", input: "SELECT @a, '"},
		{name: "escape string with escaped quote", input: `SELECT E'abc\', @a`},
		{name: "escape string ending in backslash", input: `SELECT E'\`},
		{name: "quoted identifier", input: `SELECT "abc, @a`},
		{name: "block comment", input: "SELECT @a /* @b"},
		{name: "nested block comment", input: "SELECT /* /* */ @a"},
		{name: "block comment slash star slash", input: "SELECT /*/ @a"},
		{name: "dollar quote", input: "SELECT $$ @a"},
		{name: "tagged dollar quote", input: "SELECT $fn$ @a $fn"},
		{name: "dollar quote tag is case sensitive", input: "SELECT $fn$ @a $FN$"},
		{name: "utf8 inside unterminated string", input: "SELECT 'héllo"},
		{name: "large unterminated string", input: "SELECT '" + strings.Repeat("é@a ", 1<<18)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := runBounded(t, tt.input)
			require.ErrorIs(t, err, errUnterminatedConstruct)
			assert.Nil(t, stmt)
		})
	}
}

func TestPrepareStatementRejectsAmbiguousOperators(t *testing.T) {
	stmt, err := runBounded(t, "SELECT * FROM t WHERE x<@name")
	require.EqualError(t, err, `prepare statement: ambiguous operator before named parameter: "<@name" at byte 23 `+
		`reads as operator "<@" or as "<" followed by parameter "@name"; separate them with whitespace`)
	require.ErrorIs(t, err, errAmbiguousOperator)
	assert.Nil(t, stmt)

	const containedBy, match, length, paramQ = "<@", "@@", "@-@", "@q"
	tests := []struct {
		name      string
		input     string
		operator  string
		parameter string
	}{
		{name: "contained by after space", input: "SELECT x <@lim", operator: containedBy, parameter: "@lim"},
		{name: "length", input: "SELECT @-@len", operator: length, parameter: "@len"},
		{name: "length after operand", input: "SELECT x@-@len", operator: length, parameter: "@len"},
		{name: "match", input: "SELECT x@@q", operator: match, parameter: paramQ},
		{name: "match at start", input: "@@q", operator: match, parameter: paramQ},
		{name: "match before function", input: "SELECT d @@to_tsquery(@r)", operator: match, parameter: "@to_tsquery"},
		{name: "match repeated", input: "SELECT d @@ @@q", operator: match, parameter: paramQ},
		{name: "deprecated match", input: "SELECT d @@@q", operator: "@@@", parameter: paramQ},
		{name: "after block comment", input: "SELECT x /* c */<@lim", operator: containedBy, parameter: "@lim"},
		{name: "numeric name", input: "SELECT x<@1", operator: containedBy, parameter: "@1"},
		{name: "underscore name", input: "SELECT x<@_a", operator: containedBy, parameter: "@_a"},
		{name: "utf8 name", input: "SELECT x<@名前", operator: containedBy, parameter: "@名前"},
		{name: "after valid parameters", input: "SELECT @a, '@b', x<@c", operator: containedBy, parameter: "@c"},
		{name: "before mixed placeholder", input: "SELECT x<@c, $1", operator: containedBy, parameter: "@c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := runBounded(t, tt.input)
			require.ErrorIs(t, err, errAmbiguousOperator)
			require.ErrorContains(t, err, fmt.Sprintf("reads as operator %q", tt.operator))
			require.ErrorContains(t, err, fmt.Sprintf("followed by parameter %q", tt.parameter))
			assert.Nil(t, stmt)
		})
	}
}

func TestPrepareStatementRejectsMixedPlaceholders(t *testing.T) {
	for _, input := range []string{
		"SELECT @a, $1",
		"SELECT $1, @a",
		"SELECT $10 WHERE x = @a",
		"SELECT @a::int, ($2)",
	} {
		t.Run(input, func(t *testing.T) {
			stmt, err := runBounded(t, input)
			require.ErrorIs(t, err, errMixedPlaceholders)
			assert.Nil(t, stmt)
		})
	}
}

func FuzzPrepareStatement(f *testing.F) {
	for _, seed := range []string{
		"SELECT @a, '@b', \"@c\", $$@d$$, E'\\'@e', /* /* @f */ */ -- @g\n@h",
		"SELECT 'unterminated",
		"SELECT $1, @a",
		"SELECT @名前::int",
		"SELECT x<@a, x @-@b, x@@c, x <@ d, x<=@e",
		"$a$ $a",
		"@",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		stmt, err := PrepareStatement(input)
		if err != nil {
			require.Nil(t, stmt)
			return
		}
		require.NotNil(t, stmt)
		assert.Equal(t, input, stmt.UnpreparedStatement())
		if !strings.Contains(input, "@") {
			assert.Equal(t, input, stmt.Revised())
		}
		total := 0
		if positions := stmt.ParameterPositions(); positions != nil {
			total = positions.totalPositions
		}
		assert.Len(t, stmt.BoundParameterValues(), total)
	})
}
