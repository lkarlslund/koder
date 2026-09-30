package domain

import "errors"

// ClientErrorCode returns the stable code a client can match on, when err or
// an error it wraps implements ClientErrorCode() string.
func ClientErrorCode(err error) string {
	var coded interface{ ClientErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ClientErrorCode()
	}
	return ""
}
