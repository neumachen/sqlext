package sqlext

import (
	"errors"
	"fmt"

	"github.com/neumachen/sqlext/internal"
)

// ColumnBinder is an interface that represents a column binder.
// It provides methods to access the column and bind the column.
type ColumnBinder interface {
	// Column returns the Column associated with this ColumnBinder.
	Column() Column

	// BindColumn binds the column to the provided MappedRow.
	// It returns an error if the binding operation fails.
	BindColumn(mappedRow MappedRow) error
}

// ColumnBindersFilterFunc decides whether FilterUsingFunc keeps
// columnBinders[i]. columnBinders is a snapshot of the collection being
// filtered; see FilterUsingFunc for what the callback may do with it.
type ColumnBindersFilterFunc func(i int, columnBinders ColumnBinders) (bool, error)

// ColumnBinders is a slice of ColumnBinder.
type ColumnBinders []ColumnBinder

// DefineColumnBinders creates a new ColumnBinders from the given ColumnBinder slice.
//
// Example:
//
//	binder1 := DefineColumnBinding(column1, binderFunc1)
//	binder2 := DefineColumnBinding(column2, binderFunc2)
//	binders := DefineColumnBinders(binder1, binder2)
func DefineColumnBinders(columnBinder ...ColumnBinder) ColumnBinders {
	columnBinders := make(ColumnBinders, len(columnBinder))
	copy(columnBinders, columnBinder)
	return columnBinders
}

// FilterUsingFunc returns a new ColumnBinders holding, in their original order,
// the binders for which filterFunc returns true. It is typically used to narrow
// a record's full set of bindings to the columns a query selects.
//
// filterFunc is called once per index, in order, with a snapshot of the
// receiver taken before filtering starts. Filtering never changes the
// snapshot, so as long as callbacks treat it as read-only, every call sees the
// original collection rather than a partially filtered one. Writes a callback
// makes to the snapshot are visible to later calls but change neither the
// receiver nor which binders are returned.
//
// The result never shares storage with the receiver, and it is empty, not nil
// and not the receiver, when nothing matches. Filtering is shallow: the
// returned binders are the receiver's binders, so they share their columns,
// setters and destinations. A nil filterFunc is an error. If filterFunc
// returns an error, filtering stops and FilterUsingFunc returns nil and that
// error unchanged.
//
// Example:
//
//	binders := DefineColumnBinders(binder1, binder2, binder3)
//	filteredBinders, err := binders.FilterUsingFunc(func(i int, cb ColumnBinders) (bool, error) {
//		return cb[i].Column() == "id", nil
//	})
//	if err != nil {
//		// Handle error
//	}
//	// filteredBinders now contains only the binders for columns named "id"
func (c ColumnBinders) FilterUsingFunc(
	filterFunc ColumnBindersFilterFunc,
) (
	ColumnBinders,
	error,
) {
	if filterFunc == nil {
		return nil, errors.New("filter column binders: nil filter func")
	}

	// original is never handed to the callback, so the binders retained below
	// are the ones the receiver held when filtering started.
	original := make(ColumnBinders, len(c))
	copy(original, c)
	snapshot := make(ColumnBinders, len(c))
	copy(snapshot, c)

	filtered := make(ColumnBinders, 0, len(c))
	for i := range original {
		keep, err := filterFunc(i, snapshot)
		if err != nil {
			return nil, err
		}
		if keep {
			filtered = append(filtered, original[i])
		}
	}

	return filtered, nil
}

// Replace returns a new ColumnBinders in which the one binder whose Column
// equals replacement.Column() is replaced by replacement. Order is preserved
// and the receiver is not modified. Use it to swap a binding, for example to
// apply a different conversion for one query, without rebuilding the
// collection. It works with both legacy and typed binders.
//
// Replace is shallow: the other binders in the result are the receiver's
// binders and share their setters and destinations.
//
// The error wraps ErrColumnMapping when replacement is nil (including a typed
// nil), has an empty column name, matches no binder or matches more than one,
// or when the receiver holds a nil binder.
func (c ColumnBinders) Replace(replacement ColumnBinder) (ColumnBinders, error) {
	if internal.IsNil(replacement) {
		return nil, fmt.Errorf("replace column binder: nil replacement: %w", ErrColumnMapping)
	}
	name := replacement.Column()
	if name == "" {
		return nil, fmt.Errorf("replace column binder: empty column name: %w", ErrColumnMapping)
	}

	index := -1
	for i, binder := range c {
		if internal.IsNil(binder) {
			return nil, fmt.Errorf("replace column binder %q: nil binder at index %d: %w", name, i, ErrColumnMapping)
		}
		if binder.Column() != name {
			continue
		}
		if index >= 0 {
			return nil, fmt.Errorf("replace column binder %q: duplicate binders: %w", name, ErrColumnMapping)
		}
		index = i
	}
	if index < 0 {
		return nil, fmt.Errorf("replace column binder %q: no binder for column: %w", name, ErrColumnMapping)
	}

	replaced := make(ColumnBinders, len(c))
	copy(replaced, c)
	replaced[index] = replacement
	return replaced, nil
}

// ColumnBinderFunc is a function type that defines the signature for binding a column to a field.
// It takes a MappedRow and a ColumnBinder as input and returns an error if the binding fails.
type ColumnBinderFunc func(mappedRow MappedRow, columnBinder ColumnBinder) error

// BindColumnToField is a generic function that creates a ColumnBinderFunc for binding a specific type T.
// It takes a bind function as input and returns a ColumnBinderFunc that handles the binding process.
//
// Parameters:
//   - bindFunc: A function that takes a value of type T and returns an error if binding fails.
//
// Returns:
//   - A ColumnBinderFunc that can be used to bind a column to a field of type T.
//
// Example usage:
//
//	intBinder := BindColumnToField(func(value int) error {
//	    // Bind the int value to a field
//	    return nil
//	})
func BindColumnToField[T any](bindFunc func(value T) error) ColumnBinderFunc {
	return func(mappedRow MappedRow, columnBinder ColumnBinder) error {
		if internal.IsNil(columnBinder) {
			return errors.New("column binder is nil")
		}

		// Every bound column must be present in the mapped row. A present
		// column holding NULL or a zero value is not an error; it is skipped
		// below.
		value, found := mappedRow.Get(columnBinder.Column())
		if !found {
			return fmt.Errorf("required column %s not found", columnBinder.Column().String())
		}

		if internal.IsNilOrZeroValue(value) {
			return nil
		}

		typedValue, ok := value.(T)
		if !ok {
			return fmt.Errorf(
				"column %s has a type of %T and does not match asserted type: %T",
				columnBinder.Column().String(),
				value,
				*new(T),
			)
		}

		if internal.IsNilOrZeroValue(typedValue) {
			return nil
		}

		return bindFunc(typedValue)
	}
}
