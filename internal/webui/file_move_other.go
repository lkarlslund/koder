//go:build !linux

package webui

import (
	"errors"
	"os"
)

func moveProjectFile(_ *os.Root, _, _ string) error {
	return errors.New("safe no-overwrite file moves currently require Linux")
}
