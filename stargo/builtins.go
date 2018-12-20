package stargo

import (
	"fmt"
	"reflect"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Builtins is a module, typically predeclared under the name "go",
// that provides access to all the Go builtin functions and types.
// Because reflect.Type is effectively a built-in type in stargo,
// many of the methods of reflect.Type are included here too.
//
// TODO: modules should have a more specific type than starlarkstruct.
var Builtins = starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
	// built-in functions
	"cap":     starlark.NewBuiltin("cap", builtin_cap),
	"close":   ValueOf(builtin_close),
	"complex": ValueOf(builtin_complex),
	"new":     ValueOf(builtin_new),
	"panic":   ValueOf(builtin_panic),
	"typeof":  ValueOf(reflect.TypeOf),

	// make
	// TODO: make these fail gracefully in all the cases where reflect.Make* panics.
	"make_slice": ValueOf(func(t reflect.Type, len, cap int) interface{} {
		return reflect.MakeSlice(t, len, cap).Interface() // TODO: make cap optional.
	}),
	"make_chan": ValueOf(func(t reflect.Type, cap int) interface{} {
		return reflect.MakeChan(t, cap).Interface() // TODO make cap optional
	}),
	"make_map": ValueOf(func(t reflect.Type) interface{} {
		return reflect.MakeMap(t).Interface() // TODO: add optional size.
	}),

	"append": starlark.NewBuiltin("append", builtin_append),
	"slice":  starlark.NewBuiltin("slice", builtin_slice),
	// TODO
	// complex: real, imag
	// map: delete
	// slice: copy
	// chan: recv, send, try_recv, try_send
	// pointer: indirect (but: want rvalue)

	// type constructors
	"map_of":   ValueOf(reflect.MapOf), // TODO: fail gracefully if key is not comparable.
	"array_of": ValueOf(reflect.ArrayOf),
	"slice_of": ValueOf(reflect.SliceOf),
	"ptr_to":   ValueOf(reflect.PtrTo),
	"chan_of":  ValueOf(reflect.ChanOf), // TODO ChanDir?? validate gracefully
	// TODO: func_of

	// built-in types
	"bool":       TypeOf(new(bool)),
	"int":        TypeOf(new(int)),
	"int8":       TypeOf(new(int8)),
	"int16":      TypeOf(new(int16)),
	"int32":      TypeOf(new(int32)),
	"int64":      TypeOf(new(int64)),
	"uint":       TypeOf(new(uint)),
	"uint8":      TypeOf(new(uint8)),
	"uint16":     TypeOf(new(uint16)),
	"uint32":     TypeOf(new(uint32)),
	"uint64":     TypeOf(new(uint64)),
	"uintptr":    TypeOf(new(uintptr)),
	"rune":       TypeOf(new(rune)),
	"byte":       TypeOf(new(byte)),
	"float32":    TypeOf(new(float32)),
	"float64":    TypeOf(new(float64)),
	"complex64":  TypeOf(new(complex64)),
	"complex128": TypeOf(new(complex128)),
	"string":     TypeOf(new(string)),
	"error":      TypeOf(new(error)),
})

func builtin_append(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s: unexpected keyword arguments", b.Name())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%s: got 0 arguments, want at least 1", b.Name())
	}
	slice, ok := args[0].(goSlice)
	if !ok {
		return nil, fmt.Errorf("%s: want slice, got %s for argument 1", b.Name(), args[0].Type())
	}
	// TODO: handle ([]byte, string...). handle variadic calls.
	vals := make([]reflect.Value, len(args)-1)
	for i, arg := range args[1:] {
		val, err := convert(arg, slice.v.Type().Elem())
		if err != nil {
			return nil, fmt.Errorf("%s: in argument %d, %v", b.Name(), i+2, err)
		}
		vals[i] = val
	}
	return wrap(reflect.Append(slice.v, vals...)), nil
}

func builtin_cap(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (_ starlark.Value, err error) {
	var v Value // TODO: check the error message
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &v); err != nil {
		return nil, err
	}
	defer func() { recover() }()
	err = fmt.Errorf("cap: invalid argument") // TODO handle preemptively
	return starlark.MakeInt(v.Reflect().Cap()), nil
}

func builtin_close(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (_ starlark.Value, err error) {
	var ch goChan
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &ch); err != nil {
		return nil, err
	}
	if ch.v.IsNil() {
		return nil, fmt.Errorf("close: close of nil channel")
	}
	defer func() { recover() }()
	err = fmt.Errorf("close: already closed")
	ch.v.Close()
	return starlark.None, nil
}

func builtin_complex(x, y float64) complex128 { return complex(x, y) }

func builtin_new(t reflect.Type) interface{} { return reflect.New(t).Interface() }

func builtin_panic(x interface{}) { panic(x) }

func builtin_slice(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (_ starlark.Value, err error) {
	var s goSlice
	var start, end, cap int
	cap = -1
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &s, &start, &end, &cap); err != nil {
		return nil, err
	}
	// TODO: check bounds.
	if cap < 0 {
		return wrap(s.v.Slice(start, end)), nil
	} else {
		return wrap(s.v.Slice3(start, end, cap)), nil
	}
}
