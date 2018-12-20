# Tests of slices.

load("assert.star", "assert")

tslice = go.slice_of(go.string)
assert.eq(str(tslice), '[]string')

s = go.make_slice(tslice, 0, 10) # TODO make these optional
assert.eq(len(s), 0)
assert.eq(go.cap(s), 10)

s2 = go.append(s, "one", "two", "three")
assert.eq(len(s2), 3)
assert.eq(len(s), 0) # original is unchanged
assert.eq(str(s2), '[one two three]')

s3 = go.append(s, "ONE")
assert.eq(len(s3), 1)
assert.eq(str(s3), '[ONE]')
assert.eq(str(s2), '[ONE two three]') # s3 aliases s2

# go.slice is iterable
assert.eq([x for x in s2], ["ONE", 'two', 'three'])

# variadic append
s2 = go.append(s2, *s2)
assert.eq(str(s2), '[ONE two three ONE two three]')

# slices are not sliceable
# This is intentional, to avoid confusion over the 
# differences between Go's (alias) and Starlark's (copy) semantics.
assert.fails(lambda: s2[:2], 'invalid slice operand')
