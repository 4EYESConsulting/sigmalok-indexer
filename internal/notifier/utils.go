package notifier

import (
	"fmt"
	"strings"
)

func formatTokenAmount(amount int) string {
	tokenAmount := float64(amount) / 1000000.0

	var formatted string
	if tokenAmount == float64(int64(tokenAmount)) {
		formatted = fmt.Sprintf("%.0f", tokenAmount)
	} else {
		formatted = strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", tokenAmount), "0"), ".")
	}

	return addCommas(formatted)
}

func addCommas(s string) string {
	parts := strings.Split(s, ".")
	intPart := parts[0]

	if len(intPart) <= 3 {
		return s
	}

	var result strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}

	if len(parts) > 1 {
		result.WriteString(".")
		result.WriteString(parts[1])
	}

	return result.String()
}
