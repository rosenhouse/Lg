package model

// IsolatedFailures gives the index of each failing conclusion whose
// neighbours both succeeded.
func IsolatedFailures(conclusions []string) []int {
	var isolated []int
	for i := 1; i < len(conclusions)-1; i++ {
		if failing(conclusions[i]) && conclusions[i-1] == "success" && conclusions[i+1] == "success" {
			isolated = append(isolated, i)
		}
	}
	return isolated
}
