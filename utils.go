package novis

import (
	"math/rand"
	"strings"
)

// isPathInList - If a path is in list
func isPathInList(l []string, d string) bool {
	for _, p := range l {
		if strings.HasPrefix(strings.ToLower(d), strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// removeEmptyStr - Remove empty str from list
func removeEmptyStr(l []string) []string {
	r := []string{}
	for _, s := range l {
		if len(s) > 0 {
			r = append(r, s)
		}
	}
	return r
}

// randomIntegerInRange - Generate integer between min and max
func randomIntegerInRange(min int, max int) int {
	return min + rand.Intn(max-min)
}
