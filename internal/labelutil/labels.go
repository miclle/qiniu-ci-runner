package labelutil

import "strings"

func Match(jobLabels, required []string) bool {
	if len(jobLabels) == 0 {
		return true
	}
	available := map[string]bool{}
	for _, label := range required {
		available[strings.ToLower(strings.TrimSpace(label))] = true
	}
	for _, label := range jobLabels {
		if !available[strings.ToLower(strings.TrimSpace(label))] {
			return false
		}
	}
	return true
}
