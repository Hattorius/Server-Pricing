package normalize

func OptionalIfNonZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}

	value := v
	return &value
}
