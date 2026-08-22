package kpackage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	pathpkg "path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// ErrRejectedContent means a package cannot proceed to preview or staging because
// classification found a prohibited secret or an executable/active asset.
var ErrRejectedContent = errors.New("knowledge package contains prohibited or unsafe content")

const redactionMarker = "[REDACTED]"

// RedactedField identifies one field suppressed in its entirety after a prohibited
// finding. Whole-field replacement prevents surrounding private-key or credential
// material from leaking when a detector locates only a header or label.
type RedactedField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// ScanReport contains only locations, rules, safe redactions, and aggregate policy.
// It never retains an unredacted value that triggered a rejection.
type ScanReport struct {
	Decision       knowledge.ClassificationDecision  `json:"decision"`
	Findings       []knowledge.ClassificationFinding `json:"findings,omitempty"`
	RedactedFields []RedactedField                   `json:"redacted_fields,omitempty"`
}

// ScanError exposes a safe report while preserving a stable sentinel for callers.
type ScanError struct {
	Report ScanReport
}

func (e *ScanError) Error() string { return ErrRejectedContent.Error() }
func (e *ScanError) Unwrap() error { return ErrRejectedContent }

type scanCandidate struct {
	prefix string
	value  any
	risk   []knowledge.RiskClass
}

// Scan classifies all imported package text and rejects active or executable assets.
// Validation must run first. A review decision is returned without an error so a later
// import preview can ask for explicit approval; rejected packages cannot be staged.
func Scan(ctx context.Context, pkg ValidatedPackage, classifier knowledge.Classifier) (ScanReport, error) {
	if classifier == nil {
		classifier = knowledge.RuleClassifier{}
	}
	if err := ctx.Err(); err != nil {
		return ScanReport{}, err
	}

	metadata := pkg.Manifest
	metadata.Chunk = ManifestChunk{}
	metadata.Signature = nil
	candidates := []scanCandidate{
		{prefix: "manifest", value: metadata},
		{prefix: "manifest.chunk", value: pkg.Manifest.Chunk, risk: pkg.Manifest.Chunk.Risk},
	}
	for index := range pkg.Entries {
		candidates = append(candidates, scanCandidate{
			prefix: fmt.Sprintf("entries[%d]", index), value: pkg.Entries[index], risk: pkg.Entries[index].Risk,
		})
	}
	for index := range pkg.Links {
		candidates = append(candidates, scanCandidate{prefix: fmt.Sprintf("links[%d]", index), value: pkg.Links[index]})
	}
	for index := range pkg.Evidence {
		candidates = append(candidates, scanCandidate{prefix: fmt.Sprintf("evidence[%d]", index), value: pkg.Evidence[index]})
	}

	fieldValues := make(map[string]string)
	report := ScanReport{Decision: knowledge.ClassificationDecisionAllow}
	for _, candidate := range candidates {
		fields, err := classificationFields(candidate.prefix, candidate.value)
		if err != nil {
			return ScanReport{}, fmt.Errorf("prepare knowledge package scan: %w", err)
		}
		if err := classifyFields(ctx, classifier, candidate.prefix, fields, candidate.risk, fieldValues, &report); err != nil {
			return ScanReport{}, err
		}
	}

	assetPaths := make([]string, 0, len(pkg.Assets))
	for path := range pkg.Assets {
		assetPaths = append(assetPaths, path)
	}
	sort.Strings(assetPaths)
	for _, path := range assetPaths {
		data := pkg.Assets[path]
		mediaType := assetMediaType(pkg.Manifest, path)
		fields := []knowledge.ClassificationField{
			{Name: "assets[" + path + "].path", Value: path},
			{Name: "assets[" + path + "].media_type", Value: mediaType},
		}
		if content := scannableAssetText(data); content != "" {
			fields = append(fields, knowledge.ClassificationField{Name: "assets[" + path + "].content", Value: content})
		}
		if err := classifyFields(ctx, classifier, "assets["+path+"]", fields, nil, fieldValues, &report); err != nil {
			return ScanReport{}, err
		}
		if rule := unsafeAssetRule(path, mediaType, data); rule != "" {
			report.Decision = knowledge.ClassificationDecisionReject
			report.Findings = append(report.Findings, knowledge.ClassificationFinding{
				Kind: knowledge.FindingKindSecuritySensitive, Field: path, Rule: rule,
			})
		}
	}

	sortScanFindings(report.Findings)
	report.RedactedFields = redactFields(fieldValues, report.Findings)
	if report.Decision == knowledge.ClassificationDecisionReject {
		return report, &ScanError{Report: report}
	}
	if report.Decision != knowledge.ClassificationDecisionAllow && report.Decision != knowledge.ClassificationDecisionReview {
		return ScanReport{}, errors.New("knowledge package classifier returned an invalid decision")
	}
	return report, nil
}

func classifyFields(
	ctx context.Context,
	classifier knowledge.Classifier,
	prefix string,
	fields []knowledge.ClassificationField,
	risk []knowledge.RiskClass,
	fieldValues map[string]string,
	report *ScanReport,
) error {
	for _, field := range fields {
		fieldValues[field.Name] = field.Value
	}
	result, err := classifier.Classify(ctx, knowledge.ClassificationInput{Fields: fields, Risk: risk})
	if err != nil {
		return fmt.Errorf("classify knowledge package content: %w", err)
	}
	if result.Decision != knowledge.ClassificationDecisionAllow &&
		result.Decision != knowledge.ClassificationDecisionReview &&
		result.Decision != knowledge.ClassificationDecisionReject {
		return errors.New("knowledge package classifier returned an invalid decision")
	}
	if result.Decision > report.Decision {
		report.Decision = result.Decision
	}
	for _, finding := range result.Findings {
		if finding.Field == "" {
			finding.Field = prefix
		}
		report.Findings = append(report.Findings, finding)
	}
	return nil
}

func classificationFields(prefix string, value any) ([]knowledge.ClassificationField, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	fields := make([]knowledge.ClassificationField, 0)
	collectStringFields(prefix, document, &fields)
	slices.SortFunc(fields, func(left, right knowledge.ClassificationField) int {
		return strings.Compare(left.Name, right.Name)
	})
	return fields, nil
}

func collectStringFields(path string, value any, fields *[]knowledge.ClassificationField) {
	switch value := value.(type) {
	case string:
		*fields = append(*fields, knowledge.ClassificationField{Name: path, Value: value})
	case []any:
		for index, item := range value {
			collectStringFields(fmt.Sprintf("%s[%d]", path, index), item, fields)
		}
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := key
			if path != "" {
				child = path + "." + key
			}
			collectStringFields(child, value[key], fields)
		}
	}
}

func assetMediaType(manifest Manifest, path string) string {
	for _, file := range manifest.Files {
		if file.Path == path {
			return file.MediaType
		}
	}
	return ""
}

func unsafeAssetRule(path, mediaType string, data []byte) string {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return "unsafe_asset_media_type"
	}
	base = strings.ToLower(base)
	switch base {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/javascript", "application/javascript", "application/ecmascript", "text/ecmascript", "application/wasm", "application/x-executable", "application/x-sharedlib", "application/x-msdownload", "application/vnd.android.package-archive", "application/java-archive":
		return "active_asset_media_type"
	}
	switch strings.ToLower(pathpkg.Ext(path)) {
	case ".html", ".htm", ".xhtml", ".svg", ".js", ".mjs", ".cjs", ".wasm", ".exe", ".dll", ".com", ".scr", ".msi", ".apk", ".jar", ".class", ".so", ".dylib", ".appimage", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd", ".desktop":
		return "active_asset_extension"
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if executableMagic(trimmed) {
		return "executable_asset_content"
	}
	lower := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html")) ||
		bytes.HasPrefix(lower, []byte("<script")) || bytes.HasPrefix(lower, []byte("<svg")) {
		return "active_asset_content"
	}
	return ""
}

// scannableAssetText also extracts printable strings from binary assets so a secret
// cannot evade policy merely by using application/octet-stream or surrounding bytes.
func scannableAssetText(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}
	var output strings.Builder
	for start := 0; start < len(data); {
		for start < len(data) && (data[start] < 0x20 || data[start] > 0x7e) {
			start++
		}
		end := start
		for end < len(data) && data[end] >= 0x20 && data[end] <= 0x7e {
			end++
		}
		if end-start >= 8 {
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.Write(data[start:end])
		}
		start = end + 1
	}
	return output.String()
}

func executableMagic(data []byte) bool {
	if bytes.HasPrefix(data, []byte("#!")) || bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) ||
		bytes.HasPrefix(data, []byte("MZ")) || bytes.HasPrefix(data, []byte{0, 'a', 's', 'm'}) {
		return true
	}
	if len(data) < 4 {
		return false
	}
	magic := [4]byte{data[0], data[1], data[2], data[3]}
	switch magic {
	case [4]byte{0xfe, 0xed, 0xfa, 0xce}, [4]byte{0xce, 0xfa, 0xed, 0xfe},
		[4]byte{0xfe, 0xed, 0xfa, 0xcf}, [4]byte{0xcf, 0xfa, 0xed, 0xfe},
		[4]byte{0xca, 0xfe, 0xba, 0xbe}, [4]byte{0xbe, 0xba, 0xfe, 0xca}:
		return true
	default:
		return false
	}
}

func redactFields(values map[string]string, findings []knowledge.ClassificationFinding) []RedactedField {
	seen := make(map[string]struct{})
	for _, finding := range findings {
		if !prohibitedFinding(finding.Kind) || finding.Field == "" {
			continue
		}
		if _, exists := values[finding.Field]; !exists {
			continue
		}
		seen[finding.Field] = struct{}{}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	result := make([]RedactedField, 0, len(fields))
	for _, field := range fields {
		result = append(result, RedactedField{Field: field, Value: redactionMarker})
	}
	return result
}

func prohibitedFinding(kind knowledge.FindingKind) bool {
	return kind == knowledge.FindingKindPrivateKey || kind == knowledge.FindingKindCredential || kind == knowledge.FindingKindAuthToken
}

func sortScanFindings(findings []knowledge.ClassificationFinding) {
	slices.SortStableFunc(findings, func(left, right knowledge.ClassificationFinding) int {
		if result := strings.Compare(left.Field, right.Field); result != 0 {
			return result
		}
		if left.Start != right.Start {
			return left.Start - right.Start
		}
		if left.End != right.End {
			return left.End - right.End
		}
		return strings.Compare(left.Rule, right.Rule)
	})
}
