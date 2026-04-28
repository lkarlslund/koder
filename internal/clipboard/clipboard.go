package clipboard

import (
	"errors"
	"sync"

	xclipboard "golang.design/x/clipboard"
)

var (
	initOnce sync.Once
	initErr  error

	newWaylandReader = func() waylandReader {
		return NewWayland()
	}
	xclipboardInit  = xclipboard.Init
	xclipboardRead  = xclipboard.Read
	xclipboardWrite = xclipboard.Write
)

type waylandReader interface {
	Supported() bool
	ReadText() (string, error)
	ReadImage() ([]byte, error)
}

func Init() error {
	initOnce.Do(func() {
		initErr = xclipboardInit()
	})
	return initErr
}

func ReadText() (string, error) {
	if text, ok, err := readTextWaylandFirst(); ok || err != nil {
		return text, err
	}
	return readTextXClipboard()
}

func ReadImage() ([]byte, error) {
	if data, ok, err := readImageWaylandFirst(); ok || err != nil {
		return data, err
	}
	return readImageXClipboard()
}

func WriteText(text string) error {
	if err := Init(); err != nil {
		return err
	}
	xclipboardWrite(xclipboard.FmtText, []byte(text))
	return nil
}

func Supported() bool {
	if newWaylandReader().Supported() {
		return true
	}
	return Init() == nil
}

func readTextWaylandFirst() (string, bool, error) {
	reader := newWaylandReader()
	if !reader.Supported() {
		return "", false, nil
	}
	text, err := reader.ReadText()
	if err == nil {
		if text != "" {
			return text, true, nil
		}
		return "", false, nil
	}
	if errors.Is(err, ErrClipboardEmpty) || errors.Is(err, ErrWaylandUnsupported) {
		return "", false, nil
	}
	text, fallbackErr := readTextXClipboard()
	if fallbackErr == nil && text != "" {
		return text, true, nil
	}
	return "", true, err
}

func readImageWaylandFirst() ([]byte, bool, error) {
	reader := newWaylandReader()
	if !reader.Supported() {
		return nil, false, nil
	}
	data, err := reader.ReadImage()
	if err == nil {
		if len(data) > 0 {
			return data, true, nil
		}
		return nil, false, nil
	}
	if errors.Is(err, ErrClipboardEmpty) || errors.Is(err, ErrWaylandUnsupported) {
		return nil, false, nil
	}
	data, fallbackErr := readImageXClipboard()
	if fallbackErr == nil && len(data) > 0 {
		return data, true, nil
	}
	return nil, true, err
}

func readTextXClipboard() (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	data := xclipboardRead(xclipboard.FmtText)
	if len(data) == 0 {
		return "", nil
	}
	return string(data), nil
}

func readImageXClipboard() ([]byte, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	data := xclipboardRead(xclipboard.FmtImage)
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}
