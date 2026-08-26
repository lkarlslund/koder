package memorytool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type packageDiscardResult struct {
	StageID   string `json:"stage_id"`
	Discarded bool   `json:"discarded"`
}

type packageExportResult struct {
	Path string `json:"path"`
	memoryService.ExportPackageResult
}

func normalizePackageReadArgs(args map[string]string, action string) (map[string]string, error) {
	path := tools.NormalizePathInput(args["path"])
	if path == "" {
		return nil, errors.New("path is required for memory package import")
	}
	out := map[string]string{"action": action, "path": path}
	if action == "package_stage" {
		policy := memoryService.ImportConflictPolicy(strings.TrimSpace(args["conflict_policy"]))
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		if policy != memoryService.ImportConflictPolicyUnspecified {
			out["conflict_policy"] = string(policy)
		}
		if err := normalizeBool(args, out, "review_approved"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func normalizePackageStageArgs(args map[string]string, action string) (map[string]string, error) {
	stageID := strings.TrimSpace(args["stage_id"])
	if stageID == "" {
		return nil, errors.New("stage_id is required for memory package activation or discard")
	}
	if err := (memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: stageID}).Validate(); err != nil {
		return nil, fmt.Errorf("invalid memory package stage_id: %w", err)
	}
	return map[string]string{"action": action, "stage_id": stageID}, nil
}

func normalizePackageExportArgs(args map[string]string) (map[string]string, error) {
	id := strings.TrimSpace(args["id"])
	path := tools.NormalizePathInput(args["path"])
	if id == "" || path == "" {
		return nil, errors.New("id and path are required for memory package export")
	}
	if err := (memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: id}).Validate(); err != nil {
		return nil, fmt.Errorf("invalid memory chunk id: %w", err)
	}
	out := map[string]string{"action": "package_export", "id": id, "path": path}
	if err := normalizeBool(args, out, "include_personal"); err != nil {
		return nil, err
	}
	return out, nil
}

func callPackageRead(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, runtime tools.Runtime, args map[string]string) (any, error) {
	if err := requirePackageRuntime(runtime); err != nil {
		return nil, err
	}
	abs, _, err := tools.ResolvePath(runtime, args["path"], accesssettings.AccessRead)
	if err != nil {
		return nil, err
	}
	archive, err := readBoundedPackage(abs)
	if err != nil {
		return nil, err
	}
	pkg, err := service.ValidateImportArchive(ctx, archive)
	if err != nil {
		return nil, err
	}
	if err := requireAllowedScope(offer, pkg.Manifest.Chunk.Scope); err != nil {
		return nil, err
	}
	if args["action"] == "package_preview" {
		return service.PreviewImport(ctx, pkg)
	}
	stage, err := service.StageImport(ctx, memoryService.StageImportRequest{
		Package: pkg, ReviewApproved: boolArg(args, "review_approved"),
		ConflictPolicy: memoryService.ImportConflictPolicy(args["conflict_policy"]),
	})
	if err != nil {
		return stage, err
	}
	return stage, nil
}

func callPackageExport(ctx context.Context, service *memoryService.Service, runtime tools.Runtime, args map[string]string) (packageExportResult, error) {
	if err := requirePackageRuntime(runtime); err != nil {
		return packageExportResult{}, err
	}
	abs, label, err := tools.ResolvePath(runtime, args["path"], accesssettings.AccessWrite)
	if err != nil {
		return packageExportResult{}, err
	}
	if _, err := os.Stat(abs); err == nil {
		return packageExportResult{}, fmt.Errorf("%w: memory package export refuses to overwrite existing file %s", memoryStoreAPI.ErrConflict, label)
	} else if !errors.Is(err, os.ErrNotExist) {
		return packageExportResult{}, fmt.Errorf("inspect memory package export path: %w", err)
	}
	var archive bytes.Buffer
	result, err := service.ExportPackage(ctx, &archive, memoryService.ExportPackageRequest{
		ChunkID: memory.ChunkID(args["id"]), IncludePersonal: boolArg(args, "include_personal"),
	})
	if err != nil {
		return packageExportResult{}, err
	}
	if err := writeNewPackage(abs, archive.Bytes()); err != nil {
		if errors.Is(err, os.ErrExist) {
			return packageExportResult{}, fmt.Errorf("%w: memory package export refuses to overwrite existing file %s", memoryStoreAPI.ErrConflict, label)
		}
		return packageExportResult{}, fmt.Errorf("write memory package: %w", err)
	}
	return packageExportResult{Path: label, ExportPackageResult: result}, nil
}

func writeNewPackage(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func requirePackageRuntime(runtime tools.Runtime) error {
	if runtime.ChatID == "" || strings.TrimSpace(runtime.Workdir) == "" {
		return fmt.Errorf("%w: memory package actions require a persistent chat workspace", memoryService.ErrToolOfferDenied)
	}
	return nil
}

func readBoundedPackage(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("memory package path is not a regular file")
	}
	if info.Size() > kpackage.HardMaxArchiveBytes {
		return nil, fmt.Errorf("%w: compressed archive exceeds %d bytes", kpackage.ErrLimitExceeded, kpackage.HardMaxArchiveBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, kpackage.HardMaxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > kpackage.HardMaxArchiveBytes {
		return nil, fmt.Errorf("%w: compressed archive exceeds %d bytes", kpackage.ErrLimitExceeded, kpackage.HardMaxArchiveBytes)
	}
	return data, nil
}
