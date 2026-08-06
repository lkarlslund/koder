package tools

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ToolInt is the canonical integer representation used by tool arguments.
type ToolInt int

// ParseToolInt accepts JSON integer syntax and integral decimal/exponent forms
// sometimes emitted by providers, such as "2000.00000" and "2e3".
func ParseToolInt(raw string) (ToolInt, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("empty integer value")
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return ToolInt(parsed), nil
	}
	number, _, err := big.ParseFloat(value, 10, 256, big.ToNearestEven)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value %q", raw)
	}
	integer, accuracy := number.Int(nil)
	if accuracy != big.Exact || !integer.IsInt64() {
		return 0, fmt.Errorf("invalid integer value %q", raw)
	}
	parsed := integer.Int64()
	if strconv.IntSize == 32 && (parsed < -1<<31 || parsed > 1<<31-1) {
		return 0, fmt.Errorf("invalid integer value %q", raw)
	}
	return ToolInt(parsed), nil
}

func (v ToolInt) Int() int       { return int(v) }
func (v ToolInt) String() string { return strconv.Itoa(v.Int()) }

func ParseFlexibleInt(raw string) (int, error) {
	value, err := ParseToolInt(raw)
	return value.Int(), err
}
