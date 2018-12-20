load("assert.star", "assert")

# TODO: implement complex arithmetic
# TODO: real, imag

# complex built-in function
assert.eq('go.func<func(float64, float64) complex128>', type(go.complex))
assert.eq('go.starlark.net/stargo.builtin_complex', str(go.complex))
# assert.eq(1, go.complex(1, 2)) # TODO: convert int to float
assert.eq('(1+2i)', str(go.complex(1.0, 2.0)))
assert.eq('go.complex<complex128>', type(go.complex(1.0, 2.0)))
assert.eq(go.complex128, go.typeof(go.complex(1.0, 2.0)))

# complex128 type
assert.eq('go.type<complex128>', type(go.complex128))
assert.eq('(0+0i)', str(go.complex128())) # zero value
assert.eq('(0+0i)', str(go.complex128(go.complex64()))) # conversion

assert.eq('(4+5i)', str(go.complex(4.0, 5.0)))

# assert.eq(go.complex(4.0, 5.0), go.complex(4.0, 5.0)) # TODO: fix

assert.fails(lambda: go.complex(4.0, 5.0) < go.complex(4.0, 5.0), 'invalid comparison')



