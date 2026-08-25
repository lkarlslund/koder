package attachment

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"strconv"

	"github.com/gabriel-vasile/mimetype"
)

const portablePixmapMIME = "image/x-portable-pixmap"

// LoadImage reads an image using its content rather than its filename. Binary
// PPM images, commonly produced by QEMU's screendump command, are converted to
// PNG because browsers and chat-completion image inputs do not support PPM.
func LoadImage(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return PrepareImage(data)
}

// PrepareImage validates image bytes and normalizes formats that providers do
// not accept directly. Its MIME result is derived from content, not metadata.
func PrepareImage(data []byte) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image is empty")
	}
	mimeType := mimetype.Detect(data).String()
	if isBinaryPortablePixmap(data) || mimeType == portablePixmapMIME {
		converted, err := portablePixmapToPNG(data)
		if err != nil {
			return nil, "", fmt.Errorf("decode portable pixmap: %w", err)
		}
		return converted, "image/png", nil
	}
	if ClassifyMIME(mimeType) != KindImage {
		return nil, "", fmt.Errorf("unsupported image type %q", mimeType)
	}
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif":
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", mimeType, err)
		}
	}
	return data, mimeType, nil
}

func isBinaryPortablePixmap(data []byte) bool {
	return len(data) >= 3 && data[0] == 'P' && data[1] == '6' &&
		(data[2] == ' ' || data[2] == '\t' || data[2] == '\r' || data[2] == '\n')
}

func portablePixmapToPNG(data []byte) ([]byte, error) {
	cursor := 0
	next := func() (string, error) {
		for cursor < len(data) {
			switch data[cursor] {
			case ' ', '\t', '\r', '\n':
				cursor++
			case '#':
				for cursor < len(data) && data[cursor] != '\n' {
					cursor++
				}
			default:
				start := cursor
				for cursor < len(data) && data[cursor] != ' ' && data[cursor] != '\t' && data[cursor] != '\r' && data[cursor] != '\n' && data[cursor] != '#' {
					cursor++
				}
				return string(data[start:cursor]), nil
			}
		}
		return "", fmt.Errorf("unexpected end of header")
	}
	magic, err := next()
	if err != nil || magic != "P6" {
		return nil, fmt.Errorf("expected binary P6 header")
	}
	width, err := nextPositiveInt(next)
	if err != nil {
		return nil, fmt.Errorf("width: %w", err)
	}
	height, err := nextPositiveInt(next)
	if err != nil {
		return nil, fmt.Errorf("height: %w", err)
	}
	maxValue, err := nextPositiveInt(next)
	if err != nil {
		return nil, fmt.Errorf("maximum value: %w", err)
	}
	if maxValue > 255 {
		return nil, fmt.Errorf("maximum value %d is not supported", maxValue)
	}
	if cursor >= len(data) || (data[cursor] != ' ' && data[cursor] != '\t' && data[cursor] != '\r' && data[cursor] != '\n') {
		return nil, fmt.Errorf("pixel data separator is missing")
	}
	if data[cursor] == '\r' && cursor+1 < len(data) && data[cursor+1] == '\n' {
		cursor += 2
	} else {
		cursor++
	}
	pixelCount := int64(width) * int64(height)
	if pixelCount <= 0 || pixelCount > int64(^uint(0)>>1)/4 {
		return nil, fmt.Errorf("dimensions %dx%d are too large", width, height)
	}
	want := int(pixelCount * 3)
	if len(data)-cursor < want {
		return nil, fmt.Errorf("pixel data is truncated: got %d bytes, want %d", len(data)-cursor, want)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for index := 0; index < int(pixelCount); index++ {
		source := cursor + index*3
		img.SetNRGBA(index%width, index/width, color.NRGBA{
			R: scalePixmapSample(data[source], maxValue),
			G: scalePixmapSample(data[source+1], maxValue),
			B: scalePixmapSample(data[source+2], maxValue),
			A: 0xff,
		})
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return output.Bytes(), nil
}

func nextPositiveInt(next func() (string, error)) (int, error) {
	token, err := next()
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(token)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid value %q", token)
	}
	return value, nil
}

func scalePixmapSample(value byte, maxValue int) uint8 {
	if maxValue == 255 {
		return value
	}
	return uint8((int(value)*255 + maxValue/2) / maxValue)
}
