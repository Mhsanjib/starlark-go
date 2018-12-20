# Tests of map operations.
#
# TODO: helpers for conversion to/from Starlark dict.
# TODO: a means of deleting an element from a dict.
#   May need a del m[k] operator in the spec, or a convention re: None as an element.

load("assert.star", "assert")

tm = go.map_of(go.string, go.int)
assert.eq(str(tm), 'map[string]int')

m = go.make_map(tm)
m["one"] = 1
m["two"] = 2
assert.eq(str(m), 'map[one:1 two:2]')
def f(): m[1] = "one"
assert.fails(f, 'invalid map key: cannot convert int to Go string')
def f(): m["one"] = "one"
assert.fails(f, 'invalid map element: cannot convert string to Go int')
m["one"] = 1.1 # TODO: dubious value truncation
assert.eq(str(m), 'map[one:1 two:2]')

tmm = go.map_of(go.string, tm) # a map of maps
assert.eq(str(tmm), 'map[string]map[string]int')

# TODO: fail gracefully
# assert.fails(lambda: go.map_of(tm, go.string), 'invalid map key type') # panic in Go

