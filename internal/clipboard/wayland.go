package clipboard

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"strings"

	"codeberg.org/tesselslate/wl"
	"codeberg.org/tesselslate/wl-protocols/ext"
)

var (
	ErrWaylandUnsupported = errors.New("wayland clipboard unsupported")
	ErrClipboardEmpty     = errors.New("clipboard content unavailable")
)

const (
	waylandMimeImagePNG = "image/png"
	waylandMimeURIList  = "text/uri-list"
)

var waylandTextMIMEs = []string{
	"text/plain;charset=utf-8",
	"text/plain",
	"UTF8_STRING",
	"TEXT",
	"STRING",
	"COMPOUND_TEXT",
}

type Wayland struct{}

func NewWayland() *Wayland {
	return &Wayland{}
}

func (w *Wayland) Supported() bool {
	return runtime.GOOS == "linux" && os.Getenv("WAYLAND_DISPLAY") != ""
}

func (w *Wayland) ReadText() (string, error) {
	data, err := w.read(waylandTextMIMEs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (w *Wayland) ReadImage() ([]byte, error) {
	return w.read([]string{waylandMimeImagePNG})
}

func (w *Wayland) ReadFileList() ([]string, error) {
	data, err := w.read([]string{waylandMimeURIList})
	if err != nil {
		return nil, err
	}
	return parseURIList(data)
}

func (w *Wayland) read(preferredMIMEs []string) ([]byte, error) {
	if !w.Supported() {
		return nil, ErrWaylandUnsupported
	}
	conn, err := newWaylandConnection()
	if err != nil {
		return nil, err
	}
	defer conn.close()

	if err := conn.initialize(); err != nil {
		return nil, err
	}
	offer, mime, err := conn.selectOffer(preferredMIMEs)
	if err != nil {
		return nil, err
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create pipe: %w", err)
	}
	defer reader.Close()

	offer.Receive(mime, int(writer.Fd()))
	if err := conn.display.Flush(); err != nil {
		writer.Close()
		return nil, fmt.Errorf("flush receive request: %w", err)
	}
	_ = writer.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read clipboard payload: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrClipboardEmpty
	}
	return data, nil
}

type waylandOffer struct {
	offer ext.DataControlOfferV1
	mimes []string
}

type waylandConnection struct {
	display *wl.Display
	seat    wl.Seat
	seatSet bool
	manager ext.DataControlManagerV1
	mgrSet  bool
	offers  map[uint32]*waylandOffer
	current *waylandOffer
}

func newWaylandConnection() (*waylandConnection, error) {
	display, err := wl.NewDisplay("")
	if err != nil {
		return nil, fmt.Errorf("open wayland display: %w", err)
	}
	return &waylandConnection{
		display: display,
		offers:  make(map[uint32]*waylandOffer),
	}, nil
}

func (c *waylandConnection) close() {
	if c.display != nil {
		_ = c.display.Close()
	}
}

func (c *waylandConnection) initialize() error {
	registry := c.display.GetRegistry()
	registry.SetListener(wl.RegistryListener{
		Global:       c.handleGlobal,
		GlobalRemove: func(data any, self wl.Registry, name uint32) error { return nil },
	}, c)

	if err := c.display.Roundtrip(); err != nil {
		return fmt.Errorf("discover globals: %w", err)
	}
	if !c.seatSet || !c.mgrSet {
		return fmt.Errorf("%w: missing seat=%t manager=%t", ErrWaylandUnsupported, c.seatSet, c.mgrSet)
	}

	device := c.manager.GetDataDevice(c.seat)
	device.SetListener(ext.DataControlDeviceV1Listener{
		DataOffer:        c.handleDataOffer,
		Selection:        c.handleSelection,
		Finished:         func(data any, self ext.DataControlDeviceV1) error { return nil },
		PrimarySelection: func(data any, self ext.DataControlDeviceV1, id ext.DataControlOfferV1) error { return nil },
	}, c)

	if err := c.display.Roundtrip(); err != nil {
		return fmt.Errorf("read selection: %w", err)
	}
	return nil
}

func (c *waylandConnection) selectOffer(preferredMIMEs []string) (ext.DataControlOfferV1, string, error) {
	if c.current == nil {
		return ext.DataControlOfferV1{}, "", ErrClipboardEmpty
	}
	for _, preferred := range preferredMIMEs {
		for _, offered := range c.current.mimes {
			if strings.EqualFold(offered, preferred) {
				return c.current.offer, offered, nil
			}
		}
	}
	return ext.DataControlOfferV1{}, "", fmt.Errorf("%w: available mime types=%s", ErrClipboardEmpty, strings.Join(c.current.mimes, ", "))
}

func (c *waylandConnection) handleGlobal(data any, self wl.Registry, name uint32, iface string, version uint32) error {
	switch iface {
	case wl.SeatInterface.Name:
		if !c.seatSet {
			obj := self.Bind(name, &wl.SeatInterface, minUint32(version, 1))
			c.seat = wl.Seat(obj)
			c.seatSet = true
		}
	case ext.DataControlManagerV1Interface.Name:
		if !c.mgrSet {
			obj := self.Bind(name, &ext.DataControlManagerV1Interface, minUint32(version, 1))
			c.manager = ext.DataControlManagerV1(obj)
			c.mgrSet = true
		}
	}
	return nil
}

func (c *waylandConnection) handleDataOffer(data any, self ext.DataControlDeviceV1, id ext.DataControlOfferV1) error {
	offerID, ok := safeObjectID(wl.Object(id))
	if !ok {
		return nil
	}
	state := &waylandOffer{offer: id}
	c.offers[offerID] = state
	id.SetListener(ext.DataControlOfferV1Listener{
		Offer: func(data any, self ext.DataControlOfferV1, mimeType string) error {
			data.(*waylandOffer).mimes = append(data.(*waylandOffer).mimes, mimeType)
			return nil
		},
	}, state)
	return nil
}

func (c *waylandConnection) handleSelection(data any, self ext.DataControlDeviceV1, id ext.DataControlOfferV1) error {
	offerID, ok := safeObjectID(wl.Object(id))
	if !ok {
		c.current = nil
		return nil
	}
	c.current = c.offers[offerID]
	return nil
}

func parseURIList(data []byte) ([]string, error) {
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		uri, err := url.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("parse uri %q: %w", line, err)
		}
		if uri.Scheme != "file" {
			continue
		}
		path, err := url.PathUnescape(uri.Path)
		if err != nil {
			return nil, fmt.Errorf("decode uri %q: %w", line, err)
		}
		paths = append(paths, path)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func safeObjectID(object wl.Object) (id uint32, ok bool) {
	defer func() {
		if recover() != nil {
			id = 0
			ok = false
		}
	}()
	return object.GetId(), true
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
