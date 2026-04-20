package clipboard

import (
	"sync"

	xclipboard "golang.design/x/clipboard"
)

var (
	initOnce sync.Once
	initErr  error
)

func Init() error {
	initOnce.Do(func() {
		initErr = xclipboard.Init()
	})
	return initErr
}

func ReadText() (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	data := xclipboard.Read(xclipboard.FmtText)
	if len(data) == 0 {
		return "", nil
	}
	return string(data), nil
}

func ReadImage() ([]byte, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	data := xclipboard.Read(xclipboard.FmtImage)
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

func WriteText(text string) error {
	if err := Init(); err != nil {
		return err
	}
	xclipboard.Write(xclipboard.FmtText, []byte(text))
	return nil
}

func Supported() bool {
	return Init() == nil
}
