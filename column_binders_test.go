package sqlext_test

import (
	"database/sql/driver"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/neumachen/sqlext"
)

// noopInt64 builds a typed binding whose setter is irrelevant to the test.
func noopInt64(column sqlext.Column) sqlext.ColumnScanner {
	return sqlext.BindColumn(column, func(int64) error { return nil })
}

func columnNames(binders sqlext.ColumnBinders) sqlext.Columns {
	names := make(sqlext.Columns, 0, len(binders))
	for _, binder := range binders {
		names = append(names, binder.Column())
	}
	return names
}

func keepColumns(columns ...sqlext.Column) sqlext.ColumnBindersFilterFunc {
	wanted := sqlext.Columns(columns)
	return func(i int, binders sqlext.ColumnBinders) (bool, error) {
		return wanted.HasColumn(binders[i].Column()), nil
	}
}

func TestColumnBindersFilterUsingFunc(t *testing.T) {
	tests := []struct {
		name   string
		input  sqlext.ColumnBinders
		filter sqlext.ColumnBindersFilterFunc
		want   sqlext.Columns
	}{
		{
			name:   "keeps matches in original order",
			input:  sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("b"), noopInt64("c"), noopInt64("d")),
			filter: keepColumns("d", "b"),
			want:   sqlext.Columns{"b", "d"},
		},
		{
			name:   "keeps everything",
			input:  sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("b")),
			filter: keepColumns("a", "b"),
			want:   sqlext.Columns{"a", "b"},
		},
		{
			name:   "no matches yields empty",
			input:  sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("b")),
			filter: keepColumns("z"),
			want:   sqlext.Columns{},
		},
		{
			name:   "empty receiver yields empty",
			input:  sqlext.ColumnBinders{},
			filter: keepColumns("a"),
			want:   sqlext.Columns{},
		},
		{
			name:   "nil receiver yields empty",
			input:  nil,
			filter: keepColumns("a"),
			want:   sqlext.Columns{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.input)

			got, err := tt.input.FilterUsingFunc(tt.filter)
			require.NoError(t, err)
			require.NotNil(t, got, "the result is an empty collection, never nil")
			assert.Equal(t, tt.want, columnNames(got))
			assert.Equal(t, before, tt.input, "filtering must not change the receiver")
		})
	}
}

func TestColumnBindersFilterUsingFuncSeesStableSnapshot(t *testing.T) {
	binders := sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("b"), noopInt64("c"))

	var seen []sqlext.Columns
	got, err := binders.FilterUsingFunc(func(i int, view sqlext.ColumnBinders) (bool, error) {
		seen = append(seen, columnNames(view))
		return i != 0, nil
	})
	require.NoError(t, err)

	assert.Equal(t, sqlext.Columns{"b", "c"}, columnNames(got))
	require.Len(t, seen, 3)
	for i, columns := range seen {
		assert.Equal(t, sqlext.Columns{"a", "b", "c"}, columns,
			"call %d must see the original collection, not the partially filtered one", i)
	}
}

func TestColumnBindersFilterUsingFuncIgnoresCallbackMutation(t *testing.T) {
	a, b, c := noopInt64("a"), noopInt64("b"), noopInt64("c")
	binders := sqlext.DefineColumnBinders(a, b, c)
	intruder := noopInt64("intruder")

	got, err := binders.FilterUsingFunc(func(i int, view sqlext.ColumnBinders) (bool, error) {
		// A write to the callback's snapshot must not reach the receiver. A
		// write the callback makes directly to the receiver does change the
		// receiver, but not the binders FilterUsingFunc retains, which come
		// from its own copy taken before filtering started.
		view[2] = intruder
		if i == 0 {
			binders[1] = intruder
		}
		return true, nil
	})
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Same(t, a, got[0])
	assert.Same(t, b, got[1])
	assert.Same(t, c, got[2])

	assert.Same(t, intruder, binders[1], "the callback's direct write to the receiver lands")
	assert.Same(t, c, binders[2], "the write to the snapshot does not reach the receiver")
}

func TestColumnBindersFilterUsingFuncResultDoesNotAliasReceiver(t *testing.T) {
	a, b := noopInt64("a"), noopInt64("b")
	tests := []struct {
		name   string
		filter sqlext.ColumnBindersFilterFunc
	}{
		{name: "all kept", filter: keepColumns("a", "b")},
		{name: "none kept", filter: keepColumns()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binders := sqlext.DefineColumnBinders(a, b)

			got, err := binders.FilterUsingFunc(tt.filter)
			require.NoError(t, err)
			got = append(got[:0], noopInt64("x"))

			assert.Same(t, a, binders[0], "writing to the result must not reach the receiver")
			assert.Same(t, b, binders[1])
			assert.Len(t, got, 1)
		})
	}
}

func TestColumnBindersFilterUsingFuncSharesBinders(t *testing.T) {
	calls := 0
	binder := sqlext.BindColumn("a", func(int64) error { calls++; return nil })
	binders := sqlext.DefineColumnBinders(binder, noopInt64("b"))

	got, err := binders.FilterUsingFunc(keepColumns("a"))
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Same(t, binder, got[0], "filtering is shallow: binders are shared, not copied")
	require.NoError(t, got[0].(sqlext.ColumnScanner).Scan(int64(1)))
	assert.Equal(t, 1, calls)
}

func TestColumnBindersFilterUsingFuncCallbackError(t *testing.T) {
	want := errors.New("filter failed")
	binders := sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("b"), noopInt64("c"))

	calls := 0
	got, err := binders.FilterUsingFunc(func(i int, _ sqlext.ColumnBinders) (bool, error) {
		calls++
		if i == 1 {
			return true, want
		}
		return true, nil
	})

	require.ErrorIs(t, err, want)
	assert.Nil(t, got)
	assert.Equal(t, 2, calls, "filtering stops at the first callback error")
}

func TestColumnBindersFilterUsingFuncNilFilter(t *testing.T) {
	for name, binders := range map[string]sqlext.ColumnBinders{
		"empty receiver":     nil,
		"non-empty receiver": sqlext.DefineColumnBinders(noopInt64("a")),
	} {
		t.Run(name, func(t *testing.T) {
			var got sqlext.ColumnBinders
			var err error
			require.NotPanics(t, func() { got, err = binders.FilterUsingFunc(nil) })
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestColumnBindersReplace(t *testing.T) {
	a, b, c := noopInt64("a"), noopInt64("b"), noopInt64("c")
	replacement := noopInt64("b")
	binders := sqlext.DefineColumnBinders(a, b, c)

	got, err := binders.Replace(replacement)
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Same(t, a, got[0])
	assert.Same(t, replacement, got[1])
	assert.Same(t, c, got[2])

	assert.Same(t, b, binders[1], "Replace must not change the receiver")
	got[0] = noopInt64("x")
	assert.Same(t, a, binders[0], "the result must not share storage with the receiver")
}

func TestColumnBindersReplaceLegacyBinders(t *testing.T) {
	var got string
	original := sqlext.DefineColumnBinding(nameColumn, sqlext.BindColumnToField(func(string) error {
		return errors.New("original must not run")
	}))
	replacement := sqlext.DefineColumnBinding(nameColumn, sqlext.BindColumnToField(func(v string) error {
		got = v
		return nil
	}))
	binders := sqlext.DefineColumnBinders(
		sqlext.DefineColumnBinding(idColumn, sqlext.BindColumnToField(func(int64) error { return nil })),
		original,
	)

	replaced, err := binders.Replace(replacement)
	require.NoError(t, err)

	row := sqlext.MappedRow{idColumn: int64(1), nameColumn: adaName}
	for _, binder := range replaced {
		require.NoError(t, binder.BindColumn(row))
	}
	assert.Equal(t, adaName, got)
	assert.Same(t, original, binders[1])
}

func TestColumnBindersReplaceErrors(t *testing.T) {
	var typedNil *nilScanner
	tests := []struct {
		name        string
		binders     sqlext.ColumnBinders
		replacement sqlext.ColumnBinder
	}{
		{name: "nil replacement", binders: sqlext.DefineColumnBinders(noopInt64("a")), replacement: nil},
		{name: "typed nil replacement", binders: sqlext.DefineColumnBinders(noopInt64("a")), replacement: typedNil},
		{name: "empty replacement name", binders: sqlext.DefineColumnBinders(noopInt64("a")), replacement: noopInt64("")},
		{name: "no match", binders: sqlext.DefineColumnBinders(noopInt64("a")), replacement: noopInt64("z")},
		{name: "empty receiver", binders: nil, replacement: noopInt64("a")},
		{name: "duplicate matches", binders: sqlext.DefineColumnBinders(noopInt64("a"), noopInt64("a")), replacement: noopInt64("a")},
		{name: "nil entry in receiver", binders: sqlext.ColumnBinders{noopInt64("a"), nil}, replacement: noopInt64("a")},
		{name: "typed nil entry in receiver", binders: sqlext.ColumnBinders{typedNil, noopInt64("a")}, replacement: noopInt64("a")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.binders)

			var got sqlext.ColumnBinders
			var err error
			require.NotPanics(t, func() { got, err = tt.binders.Replace(tt.replacement) })
			require.ErrorIs(t, err, sqlext.ErrColumnMapping)
			assert.Nil(t, got)
			assert.Equal(t, before, tt.binders, "a failed Replace must not change the receiver")
		})
	}
}

// A struct describes all of its bindings once; a query that selects only some
// of its columns filters them to the projection, and a caller can swap one
// binding for a different conversion before building the scanner.
func TestTypedRowProjectionWithFilteredAndReplacedBindings(t *testing.T) {
	type record struct {
		ID    int64
		Name  string
		Email string
	}
	var rec record
	bindings := sqlext.DefineColumnBinders(
		sqlext.BindColumn(idColumn, func(v int64) error { rec.ID = v; return nil }),
		sqlext.BindColumn(nameColumn, func(string) error { return errors.New("replaced setter must not run") }),
		sqlext.BindColumn("email", func(v string) error { rec.Email = v; return nil }),
	)

	fixture := &scanFixture{
		columns: []string{nameColumn, idColumn},
		values:  [][]driver.Value{{adaName, int64(7)}},
	}
	rows := openScanRows(t, fixture)

	projected, err := bindings.FilterUsingFunc(keepColumns(idColumn, nameColumn))
	require.NoError(t, err)
	projected, err = projected.Replace(sqlext.BindColumn(nameColumn, func(v string) error {
		rec.Name = "name:" + v
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, bindings, 3, "the struct's full binding set is unchanged")

	scanner, err := sqlext.NewRowScanner(rows, projected)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, scanner.Scan())

	assert.Equal(t, record{ID: 7, Name: "name:" + adaName}, rec)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())

	_, err = sqlext.NewRowScanner(openScanRows(t, &scanFixture{columns: fixture.columns}), bindings)
	assert.ErrorIs(t, err, sqlext.ErrColumnMapping, "unfiltered bindings still fail exact matching")
}
