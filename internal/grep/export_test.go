package grep

// RequiredLiterals gives the literals that Compile finds that pattern's every
// match holds one of.
func RequiredLiterals(pattern string) (literals []string, fold bool) {
	m, err := Compile(pattern)
	if err != nil {
		panic(err)
	}
	for _, literal := range m.literals {
		literals = append(literals, string(literal))
	}
	return literals, m.fold
}
