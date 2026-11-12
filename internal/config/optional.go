package config

// OptionalBool is a boolean variable that may be unset; only the string "true" is true.
type OptionalBool struct {
	set   bool
	value bool
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *OptionalBool) UnmarshalText(text []byte) error {
	b.set = true
	b.value = string(text) == "true"

	return nil
}

// Or returns the value, or fallback when the variable is unset.
func (b OptionalBool) Or(fallback bool) bool {
	if !b.set {
		return fallback
	}

	return b.value
}
