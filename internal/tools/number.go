package tools

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func ParseFlexibleInt(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("empty integer value")
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed, nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("invalid integer value %q", raw)
	}
	if number != math.Trunc(number) {
		return 0, fmt.Errorf("invalid integer value %q", raw)
	}
	return int(number), nil
}
