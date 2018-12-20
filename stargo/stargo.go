// Package stargo provides Starlark bindings for Go values, variables,
// and types, allowing you to write Starlark scripts that interact with
// the full richness of Go functions and data types.
//
// See the cmd/stargo subdirectory for examples.
//
// This was entirely inspired by Nate Finch's work on Starlight.
//
// THIS IS ALL EXPERIMENTAL AND MAY CHANGE RADICALLY.
//
// TODO:
// - add (explicit) helper functions for converting Go lists to Starlark lists and back.
// - disallow access to unexported fields in all places.
// - need a way to copy values, like x := &ptr; y = &x
// - tests
// - docs
package stargo

import (
	"fmt"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// NOTES for docs (TODO):
//
// - lvalue and rvalue modes, &v, and *ptr and the
//   changes in the Starlark compiler and runtime.
//   TODO unary *x won't work: ambiguous wrt f(*args).
//   Use a go.indirect builtin. Shouldn't be needed often(?).
//
// - types are first-class values, as if reflect.Type was built-in.
//   e.g. "bytes.Buffer" is the reflect.Type for it.
//   bytes.Buffer() instantiates one
//
// - anonymous fields are promoted. (This creates potential ambiguity
//   because names are unqualified so a type can have two methods or
//   fields called f.)
//
// - there is no reasonable interpretation we can give to Freeze, so I'm
//   completely ignoring it. Caveat usor.
//
// - when an expression retrieves a primitive value of a named type
//   (e.g. token.Pos, which is a uint), we must preserve the namedness
//   because it provides all the methods. (We don't just want to conver
//   it to a uint.)  However, the primitive wrappers do not (yet)
//   participate in arithmetic.  We could support that, but we should
//   require that both operands of binary ops (+, etc) have the same
//   type. Otherwise what does celcius + fahrenheit return?
//   Named string types will not have the methods of string.
//
// - don't use "if err" (truth value) because err is
//   the concrete type, which could be zero. Use err != None.
//
// - there is no go.interface. An interface is a type, and this language is untyped.
//   All Starlark reflect.Values are rvalues or pointers; there are no lvalues
//   that might have a kind of Interface.
//
// - slice a[i:j:k] operator intentionally unimplemented to avoid
//   confusion. (It would be trivial to implement on Slice and Array.)
//   We definitely don't want to use the starlark operator with
//   Go semantics. Use go.slice operator.

// A Value is a Starlark value that wraps a Go value using reflection.
type Value interface {
	starlark.Value
	Reflect() reflect.Value
}

// ---- func ----

// A goFunc represents a Go value of kind func.
type goFunc struct {
	v reflect.Value // kind=Func, !CanAddr
}

var (
	_ starlark.Callable = goFunc{}
	_ Value             = goFunc{}
)

func (f goFunc) Hash() (uint32, error)                    { return uint32(f.v.Pointer()), nil }
func (f goFunc) Reflect() reflect.Value                   { return f.v }
func (f goFunc) Freeze()                                  {}
func (f goFunc) String() string                           { return f.Name() }
func (f goFunc) Truth() starlark.Bool                     { return f.v.IsNil() == false }
func (f goFunc) Type() string                             { return fmt.Sprintf("go.func<%s>", f.v.Type()) }
func (f goFunc) Attr(name string) (starlark.Value, error) { return method(f.v, name) }
func (f goFunc) AttrNames() []string                      { return methodNames(f.v) }

func (f goFunc) CallInternal(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (_ starlark.Value, err error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("Go function %s does not accept named arguments", f)
	}
	if f.v.IsNil() {
		return nil, fmt.Errorf("call of nil function")
	}

	ft := f.v.Type()
	arity := ft.NumIn()
	variadic := ft.IsVariadic()
	if len(args) < arity || len(args) > arity && !variadic {
		return nil, fmt.Errorf("in call to %s, got %d arguments, want %d", f, len(args), arity)
	}
	var in []reflect.Value
	for i, arg := range args {
		var t reflect.Type
		if variadic && i >= arity-1 {
			t = ft.In(arity - 1).Elem()
		} else {
			t = ft.In(i)
		}
		x, err := convert(arg, t)
		if err != nil {
			return nil, fmt.Errorf("in argument %d of call to %s, %s", i+1, f, err)
		}
		in = append(in, x)
	}

	// Handle panics in Go code.
	// TODO: don't display the whole stack, only up to this frame.
	// TODO: turn Go stack frames into Starlark ones? Perhaps too heavy-handed.
	ok := false
	defer func() {
		if !ok {
			debug.PrintStack()
			err = fmt.Errorf("panic in Go function %s: %v", f, recover())
		}
	}()
	out := f.v.Call(in)
	ok = true
	if len(out) == 0 {
		return starlark.None, nil
	}
	if len(out) == 1 {
		return wrap(out[0]), nil
	}
	tuple := make(starlark.Tuple, len(out))
	for i, r := range out {
		tuple[i] = wrap(r)
	}
	return tuple, nil
}

func (f goFunc) Name() string {
	name := runtime.FuncForPC(f.v.Pointer()).Name()
	if name == "reflect.methodValueCall" {
		name = fmt.Sprintf("%#v", f.v.Interface()) // TODO: do better.
	}
	if name == "" {
		name = str(f.v)
	}
	return name
}

// ---- complex ----

// A goComplex represents a Go value of kind complex.
type goComplex struct {
	v reflect.Value // Kind=Complex, !CanAddr
}

var (
	_ starlark.Comparable = goComplex{}
	_ starlark.HasBinary  = goComplex{}
	_ starlark.HasUnary   = goComplex{} // - +
	_ Value               = goComplex{}
)

func (c goComplex) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: %s", c.Type()) // TODO
}

func (c goComplex) Reflect() reflect.Value { return c.v }
func (c goComplex) Freeze()                {}

func (c goComplex) String() string                           { return str(c.v) }
func (c goComplex) Truth() starlark.Bool                     { return c.v.Complex() != 0 }
func (c goComplex) Type() string                             { return fmt.Sprintf("go.complex<%s>", c.v.Type()) }
func (c goComplex) Attr(name string) (starlark.Value, error) { return method(c.v, name) }
func (c goComplex) AttrNames() []string                      { return methodNames(c.v) }

func (x goComplex) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	// TODO: what about different kinds of complex?
	switch op {
	case syntax.EQL:
		return x == y, nil
	case syntax.NEQ:
		return x != y, nil
	}
	return false, fmt.Errorf("invalid comparison: %s %s %s", x.Type(), op, y.Type())
}

func (c goComplex) Binary(op syntax.Token, y starlark.Value, side starlark.Side) (starlark.Value, error) {
	// TODO: + - * / // %
	return nil, nil
}

func (c goComplex) Unary(op syntax.Token) (starlark.Value, error) {
	switch op {
	case syntax.PLUS:
		return c, nil
	case syntax.MINUS:
		// TODO: negate
	}
	return nil, nil
}

// ---- ptr ----

// A goPtr represents a Go value of kind pointer.
//
// If the variable it points to contains a struct,
// the struct's fields may be accessed or updated:
// p.f returns the value of that field, and
// p.f = x updates it.
// Similarly, a pointer to an array allows elements to be updated:
// p[i] = 1.  TODO: test p[i]+=1.
//
// Starlark has no unary * operator: it would be ambiguous in f(*args).
// Use the go.indirect builtin to load the value from the pointed-to variable.
//
// goPtrs are comparable, but they do not compare equal to None as that
// would violate symmetry: None does not compare equal to any other
// value. To test whether a pointer is valid, use its truth-value.
type goPtr struct {
	v reflect.Value // Kind=Ptr, !CanAddr
}

var (
	_ starlark.HasAttrs     = goPtr{}
	_ starlark.HasSetField  = goPtr{}
	_ starlark.HasAttrAlias = goPtr{} //  *struct
	_ starlark.Comparable   = goPtr{}
	_ Value                 = goPtr{}
	// TODO: HasSetIndex
)

func (p goPtr) AttrAlias(name string) (starlark.Value, error) {
	// This variant is called within ptr[i] = ... or ptr[i].f = ...
	v := p.v

	// methods
	if m := v.MethodByName(name); m.IsValid() {
		return goFunc{m}, nil
	}

	// struct fields (including promoted ones)
	if v.Type().Elem().Kind() == reflect.Struct {
		if v.IsNil() {
			return nil, fmt.Errorf("nil dereference")
		}
		if f := v.Elem().FieldByName(name); f.IsValid() {
			if !f.CanInterface() {
				return nil, fmt.Errorf("access to unexported field .%s", name)
			}
			return wrap(f.Addr()), nil // no copyval
		}
	}
	return nil, nil
}

func (p goPtr) Attr(name string) (starlark.Value, error) {
	v := p.v

	// methods
	if m := v.MethodByName(name); m.IsValid() {
		return goFunc{m}, nil
	}

	// struct fields (including promoted ones)
	if v.Type().Elem().Kind() == reflect.Struct {
		if v.IsNil() {
			return nil, fmt.Errorf("nil dereference")
		}
		if f := v.Elem().FieldByName(name); f.IsValid() {
			if !f.CanInterface() {
				return nil, fmt.Errorf("access to unexported field .%s", name)
			}
			return wrap(copyVal(f)), nil // copyval. TODO factor
		}
	}
	return nil, nil
}

func (p goPtr) AttrNames() []string {
	var names []string

	// methods
	t := p.v.Type()
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}

	// struct fields (including promoted ones)
	if elem := t.Elem(); elem.Kind() == reflect.Struct {
		for i := 0; i < elem.NumField(); i++ {
			names = append(names, elem.Field(i).Name)
		}
	}

	sort.Strings(names)
	return names
}

func (p goPtr) SetField(name string, val starlark.Value) error {
	if p.v.IsNil() {
		return fmt.Errorf("nil dereference")
	}
	if elem := p.v.Elem(); elem.Kind() == reflect.Struct {
		if f := elem.FieldByName(name); f.CanSet() {
			x, err := convert(val, f.Type())
			if err != nil {
				return err
			}
			f.Set(x)
			return nil
		}
	}
	return fmt.Errorf("can't set .%s field of %s", name, p.Type())
}

func (p goPtr) Hash() (uint32, error)  { return uint32(p.v.Pointer()), nil } // TODO: better hash
func (p goPtr) Reflect() reflect.Value { return p.v }
func (p goPtr) Freeze()                {}
func (p goPtr) String() string         { return str(p.v) }
func (p goPtr) Truth() starlark.Bool   { return p.v.IsNil() == false }
func (p goPtr) Type() string           { return fmt.Sprintf("go.ptr<%s>", p.v.Type()) }

func (x goPtr) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	switch op {
	case syntax.EQL:
		return x == y, nil
	case syntax.NEQ:
		return x != y, nil
	}
	return false, fmt.Errorf("invalid comparison: %s %s %s", x.Type(), op, y.Type())
}

// ---- basic ----

// A goNamedBasic represents a Go value of a named type whose
// underlying type is bool, number, or string.
//
// TODO: support arithmetic on numeric primitives, so long as both
// operands have the same type, e.g. celsius+celsius (as in Go).
//
// Named string types do not possess the methods of starlark.String as
// that would conflict with the named type's methods.
type goNamedBasic struct {
	v reflect.Value // Kind=Bool | String | Number; !CanAddr
}

var (
	_ starlark.Comparable = goNamedBasic{}
	_ starlark.HasAttrs   = goNamedBasic{}
	_ Value               = goNamedBasic{}
)

func (b goNamedBasic) Attr(name string) (starlark.Value, error) { return method(b.v, name) }
func (b goNamedBasic) AttrNames() []string                      { return methodNames(b.v) }

func (b goNamedBasic) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: %s", b.Type()) // TODO
}

func (b goNamedBasic) Reflect() reflect.Value { return b.v }
func (b goNamedBasic) Freeze()                {}
func (b goNamedBasic) String() string         { return str(b.v) }
func (b goNamedBasic) Truth() starlark.Bool   { return starlark.True } // TODO: return v != 0
func (b goNamedBasic) Type() string {
	// e.g. "go.uint<parser.Mode>".
	return fmt.Sprintf("go.%s<%s>", strings.ToLower(b.v.Kind().String()), b.v.Type())
}

func (b goNamedBasic) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	return false, fmt.Errorf("primitive comparison not implemented") // TODO
}

// ---- unsafepointer ----

// A goUnsafePointer represents a Go unsafe.Pointer value.
//
// goUnsafePointers are comparable. However, they do not compare equal to
// None (since that would violate symmetry). To test whether a pointer
// is valid, use bool(ptr).
type goUnsafePointer struct {
	v reflect.Value // Kind=UnsafePointer, !CanAddr
}

var (
	_ starlark.Comparable = goUnsafePointer{}
	_ Value               = goUnsafePointer{}
)

func (p goUnsafePointer) Hash() (uint32, error)  { return uint32(p.v.Pointer()), nil } // TODO: better hash
func (p goUnsafePointer) Reflect() reflect.Value { return p.v }
func (p goUnsafePointer) Freeze()                {}
func (p goUnsafePointer) String() string         { return str(p.v) }
func (p goUnsafePointer) Truth() starlark.Bool   { return p.v.IsNil() == false }
func (p goUnsafePointer) Type() string           { return fmt.Sprintf("go.unsafepointer<%s>", p.v.Type()) }

func (p goUnsafePointer) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	return false, fmt.Errorf("unsafe.Pointer comparison not implemented")
}

// ---- struct ----

// A goStruct represents a Go value of kind struct.
type goStruct struct {
	v reflect.Value // kind=Struct, !CanAddr
}

var (
	_ starlark.HasAttrs     = goStruct{}
	_ starlark.HasSetField  = goStruct{}
	_ starlark.HasAttrAlias = goStruct{}
	_ starlark.HasUnary     = goStruct{} // &x.f
	_ Value                 = goStruct{}
)

func (s goStruct) Unary(op syntax.Token) (starlark.Value, error) {
	// FIXME doesn't work yet
	if op == syntax.AMP {
		if s.v.CanAddr() { // FIXME
			return goPtr{s.v.Addr()}, nil
		}
		return nil, fmt.Errorf("struct has no address")
	}
	return nil, nil
}

func (s goStruct) AttrAlias(name string) (starlark.Value, error) {
	// This variant is called within x.f = ... x.f[i] = ...

	if m := s.v.MethodByName(name); m.IsValid() {
		return goFunc{m}, nil
	}
	if s.v.CanAddr() {
		ptr := s.v.Addr()
		if m := ptr.MethodByName(name); m.IsValid() {
			return goFunc{m}, nil
		}
	}

	// struct field
	if f := s.v.FieldByName(name); f.IsValid() {
		if !f.CanInterface() {
			return nil, fmt.Errorf("access to unexported field .%s", name)
		}
		if s.v.CanAddr() {
			return wrap(f.Addr()), nil // implicit &x.f
		} else {
			return wrap(f), nil // not addressable, may fail later
		}
	}
	return nil, nil
}

func (s goStruct) Attr(name string) (starlark.Value, error) {
	if m := s.v.MethodByName(name); m.IsValid() {
		return goFunc{m}, nil
	}
	// no *T methods!

	// struct field
	if f := s.v.FieldByName(name); f.IsValid() {
		return wrap(copyVal(f)), nil
	}
	return nil, nil
}

func (s goStruct) AttrNames() []string {
	t := s.v.Type()
	recv := t
	if s.v.CanAddr() {
		recv = reflect.PtrTo(t)
	}
	names := make([]string, 0, recv.NumMethod()+t.NumField())
	for i := 0; i < recv.NumMethod(); i++ {
		names = append(names, recv.Method(i).Name)
	}
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	sort.Strings(names)
	return names
}

func (s goStruct) SetField(name string, v starlark.Value) error {
	// struct field
	f := s.v.FieldByName(name)
	if !f.IsValid() {
		return fmt.Errorf("struct has no .%s field", name)
	}
	x, err := convert(v, f.Type())
	if err != nil {
		return err
	}
	if !f.CanSet() {
		// TODO isn't this inevitable because s is not addressable?
		return fmt.Errorf("can't set .%s field of struct", name)
	}
	f.Set(x)
	return nil
}

func (s goStruct) Reflect() reflect.Value { return s.v }
func (s goStruct) Freeze()                {}
func (s goStruct) Hash() (uint32, error)  { return 0, fmt.Errorf("unhashable: %s", s.Type()) } // TODO
func (s goStruct) String() string         { return str(s.v) }
func (s goStruct) Truth() starlark.Bool   { return true }
func (s goStruct) Type() string {
	suffix := ""
	if s.v.CanAddr() {
		suffix = " var" // TODO: abolish!
	}
	return fmt.Sprintf("go.struct<%s>%s", s.v.Type(), suffix)
}

// ---- array ----

// A goArray represents a Go value of kind array.
//
// To avoid confusion, goArray intentionally does not implement
// starlark.Sliceable, because s[i:j:stride] must follow the semantics
// of Starlark, not Go: the result would be a copy, not an alias, and
// the third integer operand would be a stride, not a capacity.
// Use the go.slice built-in for the Go slice operator.
type goArray struct {
	v reflect.Value // kind=Array; !CanAddr
}

var (
	_ starlark.HasAttrs       = goArray{}
	_ starlark.Comparable     = goArray{}
	_ starlark.HasSetIndex    = goArray{}
	_ starlark.IndexAliasable = goArray{}
	_ starlark.HasUnary       = goArray{} // &
	_ Value                   = goArray{}
)

// ???
func (a goArray) Unary(op syntax.Token) (starlark.Value, error) {
	if op == syntax.AMP {
		if a.v.CanAddr() {
			return goPtr{a.v.Addr()}, nil
		}
		return nil, fmt.Errorf("array has no address")
	}
	return nil, nil
}

func (a goArray) Hash() (uint32, error)                    { return 0, fmt.Errorf("unhashable: %s", a.Type()) } // TODO
func (a goArray) Attr(name string) (starlark.Value, error) { return method(a.v, name) }
func (a goArray) AttrNames() []string                      { return methodNames(a.v) }
func (a goArray) Reflect() reflect.Value                   { return a.v }
func (a goArray) Freeze()                                  {}
func (a goArray) String() string                           { return str(a.v) }
func (a goArray) Truth() starlark.Bool                     { return a.v.Len() > 0 }
func (a goArray) Type() string {
	suffix := ""
	if a.v.CanAddr() {
		suffix = " var"
	}
	return fmt.Sprintf("go.array<%s>%s", a.v.Type(), suffix)
}

func (a goArray) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	return false, fmt.Errorf("array comparison not implemented")
}

func (a goArray) IndexAlias(i int) starlark.Value {
	// This variant is called within a[i] = ... or a[i].f = ...
	elem := a.v.Index(i)
	if elem.CanAddr() {
		return wrap(elem.Addr()) // implicit &x.f
	} else {
		return wrap(elem) // not addressable -- will fail later
	}
}

func (a goArray) Index(i int) starlark.Value { return wrap(copyVal(a.v.Index(i))) }

func (a goArray) SetIndex(i int, v starlark.Value) error {
	elem := a.v.Index(i)
	x, err := convert(v, elem.Type())
	if err != nil {
		return err
	}
	if !elem.CanSet() {
		return fmt.Errorf("can't set array element")
	}
	elem.Set(x)
	return nil
}

func (a goArray) Iterate() starlark.Iterator { return &indexIterator{a.v, 0} }
func (a goArray) Len() int                   { return a.v.Len() }

type indexIterator struct {
	v reflect.Value // Kind=Array or Slice
	i int
}

func (it *indexIterator) Done() {
	// TODO: mutation check
}

func (it *indexIterator) Next(x *starlark.Value) bool {
	if it.i < it.v.Len() {
		*x = wrap(copyVal(it.v.Index(it.i)))
		it.i++
		return true
	}
	return false
}

// ---- slice ----

// A goSlice represents a Go value of kind slice.
//
// To avoid confusion, goSlice intentionally does not implement
// starlark.Sliceable, because s[i:j:stride] must follow the semantics
// of Starlark, not Go: the result would be a copy, not an alias, and
// the third integer operand would be a stride, not a capacity.
// Use the go.slice built-in for the Go slice operator.
type goSlice struct {
	v reflect.Value // kind=Slice; !CanAddr
}

var (
	_ starlark.HasAttrs    = goSlice{}
	_ starlark.HasSetIndex = goSlice{}
	_ Value                = goSlice{}
)

func (s goSlice) Attr(name string) (starlark.Value, error) { return method(s.v, name) }
func (s goSlice) AttrNames() []string                      { return methodNames(s.v) }
func (s goSlice) Reflect() reflect.Value                   { return s.v }
func (s goSlice) Freeze()                                  {}
func (s goSlice) Hash() (uint32, error)                    { return 0, fmt.Errorf("unhashable: %s", s.Type()) }
func (s goSlice) String() string                           { return str(s.v) }
func (s goSlice) Truth() starlark.Bool                     { return s.v.IsNil() == false }
func (s goSlice) Type() string                             { return fmt.Sprintf("go.slice<%s>", s.v.Type()) }

func (s goSlice) Index(i int) starlark.Value { return wrap(copyVal(s.v.Index(i))) }
func (s goSlice) SetIndex(i int, v starlark.Value) error {
	elem := s.v.Index(i)
	x, err := convert(v, elem.Type())
	if err != nil {
		return err
	}
	if !elem.CanSet() {
		return fmt.Errorf("can't set slice element")
	}
	elem.Set(x)
	return nil
}
func (s goSlice) Iterate() starlark.Iterator { return &indexIterator{s.v, 0} }
func (s goSlice) Len() int                   { return s.v.Len() }

// ---- map ----

// A goMap represents a Go value of kind map.
type goMap struct {
	v reflect.Value // kind=Map, !CanAddr
}

var (
	_ starlark.HasAttrs  = goMap{}
	_ starlark.Sequence  = goMap{}
	_ starlark.Mapping   = goMap{}
	_ starlark.HasSetKey = goMap{}
	_ Value              = goMap{}
)

func (m goMap) Attr(name string) (starlark.Value, error) { return method(m.v, name) }
func (m goMap) AttrNames() []string                      { return methodNames(m.v) }
func (m goMap) Reflect() reflect.Value                   { return m.v }
func (m goMap) Freeze()                                  {}
func (m goMap) Hash() (uint32, error)                    { return 0, fmt.Errorf("unhashable: %s", m.Type()) }
func (m goMap) String() string                           { return str(m.v) }
func (m goMap) Truth() starlark.Bool                     { return m.v.IsNil() == false }
func (m goMap) Type() string                             { return fmt.Sprintf("go.map<%s>", m.v.Type()) }

func (m goMap) Iterate() starlark.Iterator { return mapIterator{m.v.MapRange()} }
func (m goMap) Len() int                   { return m.v.Len() }

type mapIterator struct {
	it *reflect.MapIter // Kind=Map
}

func (it mapIterator) Done() {
	// TODO: mutation check
}

func (it mapIterator) Next(x *starlark.Value) bool {
	if it.it.Next() {
		*x = wrap(it.it.Key())
		return true
	}
	return false
}

func (m goMap) Get(k starlark.Value) (v starlark.Value, found bool, err error) {
	return nil, false, fmt.Errorf("m[%s] not implemented", k) // TODO
}

func (m goMap) SetKey(k, v starlark.Value) error {
	t := m.v.Type()
	kv, err := convert(k, t.Key())
	if err != nil {
		return fmt.Errorf("invalid map key: %v", err)
	}
	vv, err := convert(v, t.Elem())
	if err != nil {
		return fmt.Errorf("invalid map element: %v", err)
	}
	if m.v.IsNil() {
		return fmt.Errorf("assignment to element of nil map")
	}
	m.v.SetMapIndex(kv, vv)
	return nil
}

// ---- chan ----

// A goChan represents a Go value of kind chan.
type goChan struct {
	v reflect.Value // kind=Chan
}

var (
	_ starlark.HasAttrs = goChan{}
	_ Value             = goChan{}

	// TODO: implement Iterable (but not Sequence)
)

func (c goChan) Attr(name string) (starlark.Value, error) { return method(c.v, name) }
func (c goChan) AttrNames() []string                      { return methodNames(c.v) }
func (c goChan) Reflect() reflect.Value                   { return c.v }
func (c goChan) Freeze()                                  {}
func (c goChan) Hash() (uint32, error)                    { return 0, fmt.Errorf("unhashable: %s", c.Type()) }
func (c goChan) String() string                           { return str(c.v) }
func (c goChan) Truth() starlark.Bool                     { return c.v.IsNil() == false }
func (c goChan) Type() string                             { return fmt.Sprintf("go.chan<%s>", c.v.Type()) }

// ---- type ----

// A Type represents a Go reflect.Type value.
type Type struct {
	t reflect.Type // may be nil
}

var (
	_ starlark.HasAttrs   = Type{}
	_ starlark.Callable   = Type{}
	_ starlark.Comparable = Type{}
	_ Value               = Type{}
)

func (t Type) Hash() (uint32, error)                    { return uint32(t.Reflect().Pointer()), nil } // TODO better hash
func (t Type) String() string                           { return fmt.Sprint(t.t) }
func (t Type) Type() string                             { return fmt.Sprintf("go.type<%s>", t.t) }
func (t Type) Truth() starlark.Bool                     { return t.t != nil }
func (t Type) Reflect() reflect.Value                   { return reflect.ValueOf(t.t) }
func (t Type) Freeze()                                  {}
func (t Type) Name() string                             { return t.t.Name() }
func (t Type) Attr(name string) (starlark.Value, error) { return method(reflect.ValueOf(t.t), name) }
func (t Type) AttrNames() []string                      { return methodNames(reflect.ValueOf(t.t)) }

// Calling a type "T()" returns the zero value of T.
// Calling a type with one argument "T(x)" acts like a conversion in Go.
// Use new(T) to create a new variable of type T.
func (t Type) CallInternal(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s: unexpected keyword arguments", t)
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("%s: got %d arguments, want zero or one", t, len(args))
	}
	if len(args) == 1 {
		// T(x): conversion
		x, err := convert(args[0], t.t)
		if err != nil {
			return nil, err
		}
		return wrap(x), nil
	}
	// T(): return zero value
	return wrap(reflect.Zero(t.t)), nil
}

func (x Type) CompareSameType(op syntax.Token, y starlark.Value, depth int) (bool, error) {
	switch op {
	case syntax.EQL:
		return x == y, nil
	case syntax.NEQ:
		return x != y, nil
	}
	return false, fmt.Errorf("invalid comparison: %s %s %s", x.Type(), op, y.Type())
}

// ---- package ----

// A Package represents a Go package as a first-class Starlark value.
type Package struct {
	Path    string
	Name    string
	Members starlark.StringDict
}

var _ starlark.HasAttrs = Type{}

func (p Package) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: %s", p) }

func (p Package) String() string { return p.Path }

func (p Package) Type() string { return fmt.Sprintf("go.package<%s>", p.Path) }

func (p Package) Truth() starlark.Bool { return true }

func (p Package) Freeze() {}

func (p Package) Attr(name string) (starlark.Value, error) {
	v, ok := p.Members[name]
	if ok {
		return v, nil
	}
	return nil, nil
}

func (p Package) AttrNames() []string {
	var names []string
	for name := range p.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ---- helpers ----

// method implements the Attr method of all types except struct and
// pointer (which may have fields too).
func method(v reflect.Value, name string) (starlark.Value, error) {
	if m := v.MethodByName(name); m.IsValid() {
		return goFunc{m}, nil
	}
	return nil, nil
}

// methodNames implements the AttrNames method of all types except struct
// and pointer (which may have fields too).
func methodNames(v reflect.Value) []string {
	recv := v.Type()
	names := make([]string, 0, recv.NumMethod())
	for i := 0; i < recv.NumMethod(); i++ {
		names = append(names, recv.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// ValueOf returns a Starlark value that wraps the Go value x.
func ValueOf(x interface{}) starlark.Value {
	if x == nil {
		return starlark.None
	}
	return wrap(reflect.ValueOf(x))
}

// VarOf returns a Starlark value that wraps the Go variable ptr.
func VarOf(ptr interface{}) starlark.Value {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr {
		panic("not a pointer")
	}
	return wrap(v)
}

// TypeOf returns a Starlark value that represents the type T.
// It must be invoked as TypeOf(new(T)).
func TypeOf(ptr interface{}) starlark.Value {
	if ptr == nil {
		panic("nil")
	}
	tptr := reflect.TypeOf(ptr)
	if tptr.Kind() != reflect.Ptr {
		panic("not a pointer")
	}
	return Type{tptr.Elem()}
}

// Precondition: v.IsValid().
func copyVal(v reflect.Value) reflect.Value {
	// TODO: optimize wrap(copyval(x)).
	if !v.CanAddr() {
		return v
	}
	if v.CanInterface() {
		return reflect.ValueOf(v.Interface()) // create an rvalue copy
	}
	ptr := reflect.New(v.Type())
	ptr.Elem().Set(v) // panics if !CanSet, eg. leak of unexported field
	return ptr.Elem()

	// TODO There must be a more efficient way to shallow-copy an
	// lvalue reflect.Value and return a non-addressable rvalue.
}

// Precondition: v.IsValid().
// Precondition: !v.CanAddr() (almost)
func wrap(v reflect.Value) starlark.Value {
	if v.CanAddr() {
		// This assertion is almost right: we should never need
		// addressable values in here. We create explicit
		// pointers as needed.
		//
		// However, copyVal may create addressable values
		// unnecessarily. Can we defeat it and make this an
		// invariant?
		panic("addr")
	}
	switch v.Kind() {
	case reflect.Bool:
		if named(v.Type()) {
			return goNamedBasic{v}
		}
		return starlark.Bool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if named(v.Type()) {
			return goNamedBasic{v}
		}
		return starlark.MakeInt64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if named(v.Type()) {
			return goNamedBasic{v}
		}
		return starlark.MakeUint64(v.Uint())
	case reflect.Float32, reflect.Float64:
		if named(v.Type()) {
			return goNamedBasic{v}
		}
		return starlark.Float(v.Float())
	case reflect.String:
		if named(v.Type()) {
			return goNamedBasic{v}
		}
		return starlark.String(v.String())
	case reflect.Complex64, reflect.Complex128:
		return goComplex{v}
	case reflect.Array:
		return goArray{v}
	case reflect.Chan:
		return goChan{v}
	case reflect.Func:
		return goFunc{v}
	case reflect.Interface:
		// This means v is an lvalue.
		if v.IsNil() {
			return starlark.None
		}
		return wrap(v.Elem())
	case reflect.Map:
		return goMap{v}
	case reflect.Ptr:
		// special case:
		if v.Type() == rtype {
			return Type{v.Interface().(reflect.Type)}
		}

		return goPtr{v}
	case reflect.Slice:
		return goSlice{v}
	case reflect.Struct:
		return goStruct{v}
	case reflect.UnsafePointer:
		return goUnsafePointer{v}
	}
	panic("unknown kind: " + v.Kind().String())
}

var rtype = reflect.TypeOf(reflect.TypeOf(int(0)))  // *runtime.rtype
var eface = reflect.TypeOf(new(interface{})).Elem() // interface{}

func named(t reflect.Type) bool { return t.PkgPath() != "" }

func str(v reflect.Value) string { return fmt.Sprint(v.Interface()) }

func convert(v starlark.Value, to reflect.Type) (reflect.Value, error) {
	// This function needs a lot more rigor.

	// If the Starlark value is just a wrapper
	// around a Go value, unwrap it.
	if g, ok := v.(Value); ok {
		rv := g.Reflect()
		if rv.Type() == to {
			return rv, nil
		}
		if rv.Type().ConvertibleTo(to) {
			return rv.Convert(to), nil
		}
		return reflect.Value{}, fmt.Errorf("cannot convert %s to Go %s", v.Type(), to)
	}

	if t, ok := v.(Type); ok && to == rtype {
		return reflect.ValueOf(t.t), nil
	}

	// Allow None to convert to any pointer type.
	// A nil pointer is not equal to None, though.
	if v == starlark.None {
		switch to.Kind() {
		case reflect.Chan,
			reflect.Func,
			reflect.Interface,
			reflect.Map,
			reflect.Ptr,
			reflect.Slice,
			reflect.UnsafePointer: // TODO IsNil panics for this type
			return reflect.Zero(to), nil
		}
	}

	// TODO conversions:
	//
	// Go values -> same concrete type, alt name, alt width, eface, iface
	// non-Go values -> concrete Go type, eface
	// numeric conversions

	// TODO: pull all the Convert(to) calls to the end.

	// switch on underlying type
	switch to.Kind() {
	case reflect.Bool:
		// TODO is Truth too sloppy?
		return reflect.ValueOf(bool(v.Truth())).Convert(to), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// value must be an integer in range
		switch v := v.(type) {
		case starlark.Float:
			return reflect.ValueOf(float64(v)).Convert(to), nil // safe?
		case starlark.Int:
			i, ok := v.Int64()
			if !ok || int64(int(i)) != i {
				return reflect.Value{}, fmt.Errorf("can't convert %s to interface{}", v)
			}
			return reflect.ValueOf(int(i)).Convert(to), nil // Safe convert?
		}

	case reflect.Float32, reflect.Float64:
		if f, ok := v.(starlark.Float); ok {
			return reflect.ValueOf(f).Convert(to), nil
		}

	case reflect.String:
		if s, ok := starlark.AsString(v); ok {
			return reflect.ValueOf(s).Convert(to), nil
		}

	case reflect.Complex64, reflect.Complex128:

	case reflect.Array:

	case reflect.Chan:

	case reflect.Func:

	case reflect.Interface:
		if to == eface {
			if s, ok := starlark.AsString(v); ok {
				return reflect.ValueOf(s), nil
			}
			// Be conservative here to avoid runaway type errors.
			// Convert bool, float64, int, string as in Go.
			switch v := v.(type) {
			case starlark.String:
				return reflect.ValueOf(string(v)), nil
			case starlark.Bool:
				return reflect.ValueOf(bool(v)), nil
			case starlark.Float:
				return reflect.ValueOf(float64(v)), nil
			case starlark.Int:
				i, ok := v.Int64()
				if !ok || int64(int(i)) != i {
					return reflect.Value{}, fmt.Errorf("can't convert %s to interface{}", v)
				}
				return reflect.ValueOf(int(i)), nil
			}
			// lists, maps, etc require explicit conversion (TODO)
		} else {
			// non-empty interface that means the type must
			// have methods, so it must be a wrapper around
			// a Go value already. we should handle this
			// case at top.
		}

	case reflect.Map:

	case reflect.Ptr:

	case reflect.Slice:

	case reflect.Struct:

	case reflect.UnsafePointer:

	}

	return reflect.Value{}, fmt.Errorf("cannot convert %s to Go %s", v.Type(), to)
}
