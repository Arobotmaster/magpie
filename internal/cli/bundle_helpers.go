package cli

import "slices"

func slicesContains(s []string, v string) bool { return slices.Contains(s, v) }
func slicesDelete(s []string, v string) []string {
	return slices.DeleteFunc(slices.Clone(s), func(a string) bool { return a == v })
}
