package kpackage

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	HardMaxArchiveBytes      int64 = 256 << 20
	HardMaxUncompressedBytes int64 = 512 << 20
	HardMaxFileBytes         int64 = 64 << 20
	HardMaxFiles                   = 10000
	HardMaxPathBytes               = 1024
	HardMaxPathDepth               = 16
)

var (
	ErrInvalidArchive = errors.New("invalid memory package archive")
	ErrLimitExceeded  = errors.New("memory package archive limit exceeded")
)

type ParseLimits struct {
	MaxArchiveBytes      int64
	MaxUncompressedBytes int64
	MaxFileBytes         int64
	MaxFiles             int
	MaxPathBytes         int
	MaxPathDepth         int
}

func DefaultParseLimits() ParseLimits {
	return ParseLimits{
		MaxArchiveBytes: HardMaxArchiveBytes, MaxUncompressedBytes: HardMaxUncompressedBytes,
		MaxFileBytes: HardMaxFileBytes, MaxFiles: HardMaxFiles,
		MaxPathBytes: HardMaxPathBytes, MaxPathDepth: HardMaxPathDepth,
	}
}

type ParsedPackage struct {
	Manifest      Manifest
	manifestBytes []byte
	files         map[string][]byte
	paths         []string
	metadata      map[string]zipMetadata
}

type zipMetadata struct {
	method       uint16
	modifiedDate uint16
	modifiedTime uint16
	hasExtra     bool
}

func (p ParsedPackage) ManifestBytes() []byte {
	return slices.Clone(p.manifestBytes)
}

func (p ParsedPackage) Paths() []string {
	return slices.Clone(p.paths)
}

func (p ParsedPackage) ReadFile(name string) ([]byte, bool) {
	data, exists := p.files[name]
	return slices.Clone(data), exists
}

func ParseBytes(data []byte, limits ParseLimits) (ParsedPackage, error) {
	return Parse(bytes.NewReader(data), int64(len(data)), limits)
}

// Parse reads a structurally safe, bounded ZIP. It does not grant trust or activate
// records; schema, integrity, compatibility, and policy validation are later stages.
func Parse(reader io.ReaderAt, size int64, limits ParseLimits) (ParsedPackage, error) {
	limits, err := normalizeParseLimits(limits)
	if err != nil {
		return ParsedPackage{}, err
	}
	if reader == nil || size < 0 {
		return ParsedPackage{}, fmt.Errorf("%w: reader and non-negative size are required", ErrInvalidArchive)
	}
	if size > limits.MaxArchiveBytes {
		return ParsedPackage{}, fmt.Errorf("%w: compressed archive is %d bytes, maximum is %d", ErrLimitExceeded, size, limits.MaxArchiveBytes)
	}
	if err := validateZIPEnvelope(reader, size); err != nil {
		return ParsedPackage{}, err
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return ParsedPackage{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if len(archive.File) == 0 {
		return ParsedPackage{}, fmt.Errorf("%w: archive contains no files", ErrInvalidArchive)
	}
	if len(archive.File) > limits.MaxFiles {
		return ParsedPackage{}, fmt.Errorf("%w: archive contains %d files, maximum is %d", ErrLimitExceeded, len(archive.File), limits.MaxFiles)
	}
	if archive.File[0].Name != "manifest.json" {
		return ParsedPackage{}, fmt.Errorf("%w: manifest.json must be the first ZIP entry", ErrInvalidArchive)
	}

	files := make(map[string][]byte, len(archive.File))
	metadata := make(map[string]zipMetadata, len(archive.File))
	paths := make([]string, 0, len(archive.File))
	seen := make(map[string]string, len(archive.File))
	var total int64
	for _, file := range archive.File {
		normalizedName, err := validateArchivePath(file.Name, limits)
		if err != nil {
			return ParsedPackage{}, err
		}
		if previous, exists := seen[normalizedName]; exists {
			return ParsedPackage{}, fmt.Errorf("%w: duplicate normalized path %q and %q", ErrInvalidArchive, previous, file.Name)
		}
		seen[normalizedName] = file.Name
		if file.Flags&0x1 != 0 {
			return ParsedPackage{}, fmt.Errorf("%w: encrypted entry %q is not supported", ErrInvalidArchive, file.Name)
		}
		if file.Method != zip.Store && file.Method != zip.Deflate {
			return ParsedPackage{}, fmt.Errorf("%w: entry %q uses unsupported compression method %d", ErrInvalidArchive, file.Name, file.Method)
		}
		mode := file.Mode()
		if !mode.IsRegular() {
			return ParsedPackage{}, fmt.Errorf("%w: entry %q is not a regular file (mode %s)", ErrInvalidArchive, file.Name, mode)
		}
		if file.UncompressedSize64 > uint64(limits.MaxFileBytes) || file.UncompressedSize64 > uint64(limits.MaxUncompressedBytes-total) {
			return ParsedPackage{}, fmt.Errorf("%w: entry %q declares %d uncompressed bytes", ErrLimitExceeded, file.Name, file.UncompressedSize64)
		}
		entry, err := file.Open()
		if err != nil {
			return ParsedPackage{}, fmt.Errorf("%w: open entry %q: %v", ErrInvalidArchive, file.Name, err)
		}
		budget := min(limits.MaxFileBytes, limits.MaxUncompressedBytes-total)
		data, readErr := io.ReadAll(io.LimitReader(entry, budget+1))
		closeErr := entry.Close()
		if readErr != nil {
			return ParsedPackage{}, fmt.Errorf("%w: read entry %q: %v", ErrInvalidArchive, file.Name, readErr)
		}
		if closeErr != nil {
			return ParsedPackage{}, fmt.Errorf("%w: close entry %q: %v", ErrInvalidArchive, file.Name, closeErr)
		}
		if int64(len(data)) > budget {
			return ParsedPackage{}, fmt.Errorf("%w: entry %q exceeded its read budget", ErrLimitExceeded, file.Name)
		}
		if uint64(len(data)) != file.UncompressedSize64 {
			return ParsedPackage{}, fmt.Errorf("%w: entry %q size is %d, header declares %d", ErrInvalidArchive, file.Name, len(data), file.UncompressedSize64)
		}
		total += int64(len(data))
		files[file.Name] = data
		metadata[file.Name] = zipMetadata{method: file.Method, modifiedDate: file.ModifiedDate, modifiedTime: file.ModifiedTime, hasExtra: len(file.Extra) != 0}
		paths = append(paths, file.Name)
	}

	manifestBytes := files["manifest.json"]
	if len(manifestBytes) == 0 {
		return ParsedPackage{}, fmt.Errorf("%w: manifest.json is empty", ErrInvalidArchive)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	if err := decoder.Decode(&manifest); err != nil {
		return ParsedPackage{}, fmt.Errorf("%w: decode manifest.json: %v", ErrInvalidArchive, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ParsedPackage{}, fmt.Errorf("%w: manifest.json: %v", ErrInvalidArchive, err)
	}
	return ParsedPackage{Manifest: manifest, manifestBytes: slices.Clone(manifestBytes), files: files, paths: paths, metadata: metadata}, nil
}

func validateZIPEnvelope(reader io.ReaderAt, size int64) error {
	const (
		localHeaderSize = 4
		eocdSize        = 22
		maxCommentSize  = 1<<16 - 1
	)
	if size < eocdSize {
		return fmt.Errorf("%w: archive is too short", ErrInvalidArchive)
	}
	var first [localHeaderSize]byte
	if _, err := reader.ReadAt(first[:], 0); err != nil || !bytes.Equal(first[:], []byte{'P', 'K', 3, 4}) {
		return fmt.Errorf("%w: archive has data before its first local file header", ErrInvalidArchive)
	}
	tailSize := min(size, int64(eocdSize+maxCommentSize))
	tail := make([]byte, tailSize)
	if _, err := reader.ReadAt(tail, size-tailSize); err != nil {
		return fmt.Errorf("%w: read ZIP envelope: %v", ErrInvalidArchive, err)
	}
	index := bytes.LastIndex(tail, []byte{'P', 'K', 5, 6})
	if index < 0 || index+eocdSize > len(tail) {
		return fmt.Errorf("%w: archive has no complete end record", ErrInvalidArchive)
	}
	commentSize := int(binary.LittleEndian.Uint16(tail[index+20 : index+22]))
	if commentSize != 0 {
		return fmt.Errorf("%w: ZIP comments are not allowed", ErrInvalidArchive)
	}
	if index+eocdSize != len(tail) {
		return fmt.Errorf("%w: archive has trailing data", ErrInvalidArchive)
	}
	return nil
}

func normalizeParseLimits(value ParseLimits) (ParseLimits, error) {
	defaults := DefaultParseLimits()
	if value.MaxArchiveBytes == 0 {
		value.MaxArchiveBytes = defaults.MaxArchiveBytes
	}
	if value.MaxUncompressedBytes == 0 {
		value.MaxUncompressedBytes = defaults.MaxUncompressedBytes
	}
	if value.MaxFileBytes == 0 {
		value.MaxFileBytes = defaults.MaxFileBytes
	}
	if value.MaxFiles == 0 {
		value.MaxFiles = defaults.MaxFiles
	}
	if value.MaxPathBytes == 0 {
		value.MaxPathBytes = defaults.MaxPathBytes
	}
	if value.MaxPathDepth == 0 {
		value.MaxPathDepth = defaults.MaxPathDepth
	}
	if value.MaxArchiveBytes < 0 || value.MaxArchiveBytes > HardMaxArchiveBytes ||
		value.MaxUncompressedBytes < 0 || value.MaxUncompressedBytes > HardMaxUncompressedBytes ||
		value.MaxFileBytes < 0 || value.MaxFileBytes > HardMaxFileBytes || value.MaxFileBytes > value.MaxUncompressedBytes ||
		value.MaxFiles < 0 || value.MaxFiles > HardMaxFiles || value.MaxPathBytes < 0 || value.MaxPathBytes > HardMaxPathBytes ||
		value.MaxPathDepth < 0 || value.MaxPathDepth > HardMaxPathDepth {
		return ParseLimits{}, fmt.Errorf("%w: requested parser limits exceed hard safety bounds", ErrLimitExceeded)
	}
	return value, nil
}

func validateArchivePath(name string, limits ParseLimits) (string, error) {
	if len(name) > limits.MaxPathBytes {
		return "", fmt.Errorf("%w: ZIP path exceeds %d bytes", ErrLimitExceeded, limits.MaxPathBytes)
	}
	if name == "" || !utf8.ValidString(name) || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name {
		return "", fmt.Errorf("%w: unsafe ZIP path %q", ErrInvalidArchive, name)
	}
	components := strings.Split(name, "/")
	if len(components) > limits.MaxPathDepth {
		return "", fmt.Errorf("%w: ZIP path %q exceeds depth %d", ErrLimitExceeded, name, limits.MaxPathDepth)
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.Contains(component, ":") || strings.IndexFunc(component, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
			return "", fmt.Errorf("%w: unsafe ZIP path %q", ErrInvalidArchive, name)
		}
	}
	return norm.NFC.String(name), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("contains multiple JSON values")
	}
	return err
}
