package grep

// RequiredLiterals gives the literals Compile finds in pattern, one of which
// every match holds.
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
