package webui

import (
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func moveProjectFile(root *os.Root, source, destination string) error {
	// Pin both parents beneath the root before renameat2. Checking existence
	// then calling os.Rename could overwrite a concurrently created file.
	from, err := root.OpenFile(path.Dir(source), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	to, err := root.OpenFile(path.Dir(destination), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	err = unix.Renameat2(int(from.Fd()), path.Base(source), int(to.Fd()), path.Base(destination), unix.RENAME_NOREPLACE)
	if err != nil {
		return &os.LinkError{Op: "move", Old: source, New: destination, Err: err}
	}
	return nil
}
