package cardplatform

import (
	"fmt"
	"strings"
)

// Records written before platform classification belong to OAI. Unknown values
// are never eligible for Pro; only template editing accepts normalized input.
func IsOAI(value string) bool { return value == "" || value == "oai" }

func Normalize(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "oai"
	}
	if value != "oai" && value != "cc" {
		return "", fmt.Errorf("开卡平台只能选择 OAI 或 CC")
	}
	return value, nil
}
