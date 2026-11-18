package utils

// Require returns value if it is not the zero value of its type.
// It panics with the property name when a zero value is provided.
func Require[T comparable](name string, value T) T {
	var zero T

	if value == zero {
		panic("required property is missing: " + name)
	}

	return value
}

// Must returns value on success and panics with err on failure.
func Must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}

	return value
}
