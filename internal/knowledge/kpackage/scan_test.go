package kpackage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestScanClassifiesPackageContentAndRisk(t *testing.T) {
	t.Parallel()
	validated := validatedExample(t, canonicalExampleRequest(t))
	report, err := Scan(context.Background(), validated, knowledge.RuleClassifier{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != knowledge.ClassificationDecisionReview {
		t.Fatalf("Scan() decision = %s, want review", report.Decision)
	}
	if !slices.ContainsFunc(report.Findings, func(finding knowledge.ClassificationFinding) bool {
		return finding.Field == "manifest.chunk" && finding.Rule == "risk_physical_safety"
	}) {
		t.Fatalf("Scan() findings do not locate chunk risk: %#v", report.Findings)
	}
	if len(report.RedactedFields) != 0 {
		t.Fatalf("Scan() redacted non-prohibited review findings: %#v", report.RedactedFields)
	}
}

func TestScanRejectsAndRedactsSecretsWithoutLeakingThem(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-package-secret-84291"
	request := canonicalExampleRequest(t)
	request.Entries[0].Body += "\npassword=" + secret + "\n"
	validated := validatedExample(t, request)
	report, err := Scan(context.Background(), validated, knowledge.RuleClassifier{})
	if !errors.Is(err, ErrRejectedContent) || report.Decision != knowledge.ClassificationDecisionReject {
		t.Fatalf("Scan() report=%#v error=%v, want rejected content", report, err)
	}
	var scanError *ScanError
	if !errors.As(err, &scanError) || scanError.Report.Decision != knowledge.ClassificationDecisionReject {
		t.Fatalf("Scan() error = %#v, want ScanError", err)
	}
	encoded, marshalErr := json.Marshal(struct {
		Report ScanReport `json:"report"`
		Error  string     `json:"error"`
	}{Report: report, Error: err.Error()})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(encoded, []byte(secret)) || strings.Contains(err.Error(), secret) {
		t.Fatalf("scan diagnostics leaked rejected secret: %s", encoded)
	}
	if !slices.ContainsFunc(report.RedactedFields, func(field RedactedField) bool {
		return strings.HasSuffix(field.Field, ".body") && strings.Contains(field.Value, redactionMarker) && !strings.Contains(field.Value, secret)
	}) {
		t.Fatalf("Scan() did not provide a safe redaction: %#v", report.RedactedFields)
	}
	if !slices.ContainsFunc(validated.Entries, func(entry knowledge.Entry) bool { return strings.Contains(entry.Body, secret) }) {
		t.Fatal("Scan() mutated the validated, hash-bound package")
	}
}

func TestScanRedactsEntirePrivateKeyField(t *testing.T) {
	t.Parallel()
	const keyPayload = "c3ludGhldGljLXByaXZhdGUta2V5LXBheWxvYWQ="
	request := canonicalExampleRequest(t)
	request.Entries[0].Body += "\n-----BEGIN PRIVATE KEY-----\n" + keyPayload + "\n-----END PRIVATE KEY-----\n"
	report, err := Scan(context.Background(), validatedExample(t, request), nil)
	if !errors.Is(err, ErrRejectedContent) {
		t.Fatalf("Scan() error = %v, want ErrRejectedContent", err)
	}
	data, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(data, []byte(keyPayload)) {
		t.Fatalf("private-key payload leaked outside the matched header: %s", data)
	}
	if !slices.ContainsFunc(report.RedactedFields, func(field RedactedField) bool {
		return strings.HasSuffix(field.Field, ".body") && field.Value == redactionMarker
	}) {
		t.Fatalf("Scan() did not suppress the private-key field: %#v", report.RedactedFields)
	}
}

func TestScanRejectsSecretInTextAsset(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-asset-secret-84291"
	request := canonicalExampleRequest(t)
	request.Assets = []Asset{{Path: "assets/notes.txt", MediaType: "text/plain; charset=utf-8", Data: []byte("api_key=" + secret + "\n")}}
	report, err := Scan(context.Background(), validatedExample(t, request), nil)
	if !errors.Is(err, ErrRejectedContent) {
		t.Fatalf("Scan() error = %v, want ErrRejectedContent", err)
	}
	data, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("asset scan report leaked secret: %s", data)
	}
	if !slices.ContainsFunc(report.Findings, func(finding knowledge.ClassificationFinding) bool {
		return finding.Field == "assets[assets/notes.txt].content" && finding.Kind == knowledge.FindingKindCredential
	}) {
		t.Fatalf("Scan() findings = %#v", report.Findings)
	}
}

func TestScanRejectsSecretInMislabeledBinaryAsset(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-binary-secret-84291"
	request := canonicalExampleRequest(t)
	request.Assets = []Asset{{
		Path: "assets/photo.bin", MediaType: "application/octet-stream",
		Data: append([]byte{0xff, 0x00, 0x01}, []byte("password="+secret+"\x00")...),
	}}
	report, err := Scan(context.Background(), validatedExample(t, request), nil)
	if !errors.Is(err, ErrRejectedContent) {
		t.Fatalf("Scan() error = %v, want ErrRejectedContent", err)
	}
	data, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("binary asset scan report leaked secret: %s", data)
	}
	if !slices.ContainsFunc(report.Findings, func(finding knowledge.ClassificationFinding) bool {
		return finding.Field == "assets[assets/photo.bin].content" && finding.Kind == knowledge.FindingKindCredential
	}) {
		t.Fatalf("Scan() findings = %#v", report.Findings)
	}
}

func TestScanRejectsActiveAndExecutableAssets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		asset    Asset
		wantRule string
	}{
		{name: "active media type", asset: Asset{Path: "assets/page.dat", MediaType: "text/html", Data: []byte("safe text")}, wantRule: "active_asset_media_type"},
		{name: "active extension", asset: Asset{Path: "assets/run.js", MediaType: "text/plain", Data: []byte("ordinary")}, wantRule: "active_asset_extension"},
		{name: "mislabeled markup", asset: Asset{Path: "assets/page.txt", MediaType: "text/plain", Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")}, wantRule: "active_asset_content"},
		{name: "executable magic", asset: Asset{Path: "assets/program.bin", MediaType: "application/octet-stream", Data: []byte{0x7f, 'E', 'L', 'F', 2, 1}}, wantRule: "executable_asset_content"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := canonicalExampleRequest(t)
			request.Assets = []Asset{test.asset}
			report, err := Scan(context.Background(), validatedExample(t, request), knowledge.RuleClassifier{})
			if !errors.Is(err, ErrRejectedContent) {
				t.Fatalf("Scan() error = %v, want ErrRejectedContent", err)
			}
			if !slices.ContainsFunc(report.Findings, func(finding knowledge.ClassificationFinding) bool {
				return finding.Field == test.asset.Path && finding.Rule == test.wantRule
			}) {
				t.Fatalf("Scan() findings = %#v, want rule %q", report.Findings, test.wantRule)
			}
		})
	}
}

func TestScanHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Scan(ctx, ValidatedPackage{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan() error = %v, want context.Canceled", err)
	}
}

func TestValidatedPackageCloneDetachesNestedData(t *testing.T) {
	t.Parallel()
	request := canonicalExampleRequest(t)
	request.Assets = []Asset{{Path: "assets/note.txt", MediaType: "text/plain", Data: []byte("asset\n")}}
	for index := range request.Entries {
		request.Entries[index].Tags = []string{"clone-test"}
	}
	for index := range request.Links {
		request.Links[index].EvidenceIDs = []knowledge.EvidenceID{request.Evidence[0].ID}
	}
	original := validatedExample(t, request)
	clone := original.Clone()
	clone.Manifest.Chunk.Tags[0] = "mutated"
	clone.Entries[0].Tags[0] = "mutated"
	clone.Links[0].EvidenceIDs[0] = "01a02b00-0000-7000-8000-000000000099"
	clone.Assets["assets/note.txt"][0] = 'X'
	if original.Manifest.Chunk.Tags[0] == "mutated" || original.Entries[0].Tags[0] == "mutated" ||
		original.Links[0].EvidenceIDs[0] == clone.Links[0].EvidenceIDs[0] || original.Assets["assets/note.txt"][0] == 'X' {
		t.Fatal("ValidatedPackage.Clone() aliases nested package data")
	}
}

func validatedExample(t *testing.T, request ExportRequest) ValidatedPackage {
	t.Helper()
	validated, err := Validate(exportAndParse(t, request), ValidationOptions{CurrentKoderVersion: "r1847"})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	return validated
}
