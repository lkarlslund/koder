package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryPackages(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}

	switch {
	case r.URL.Path == memoryapi.PackagePreviewPath:
		if r.Method != http.MethodPost {
			s.writeMemoryPackageMethodError(w, requestID, http.MethodPost)
			return
		}
		if !s.requireNoMemoryQuery(w, r, requestID) {
			return
		}
		pkg, ok := s.readMemoryPackage(w, r, requestID, service)
		if !ok {
			return
		}
		preview, err := service.PreviewImport(ctx, pkg)
		if err != nil {
			s.writeMemoryServiceError(w, requestID, err)
			return
		}
		s.writeMemoryJSON(w, http.StatusOK, memoryapi.PackagePreviewResponse{
			ResponseMetadata: memoryapi.Metadata(requestID), Preview: preview,
		})
	case r.URL.Path == memoryapi.PackageStagePath:
		if r.Method != http.MethodPost {
			s.writeMemoryPackageMethodError(w, requestID, http.MethodPost)
			return
		}
		policy, reviewApproved, err := parseMemoryPackageStageQuery(r)
		if err != nil {
			s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
			return
		}
		pkg, ok := s.readMemoryPackage(w, r, requestID, service)
		if !ok {
			return
		}
		stage, err := service.StageImport(ctx, memoryService.StageImportRequest{
			Package: pkg, ConflictPolicy: policy, ReviewApproved: reviewApproved,
		})
		if err != nil {
			classified := memoryService.ClassifyError(err)
			status := memoryServiceHTTPStatus(classified.Code)
			s.writeMemoryJSON(w, status, memoryapi.PackageStageResponse{
				ResponseMetadata: memoryapi.Metadata(requestID), Preview: stage.Preview, Error: classified,
			})
			return
		}
		w.Header().Set("Location", memoryapi.PackageStageItemPath(stage.ID))
		s.writeMemoryJSON(w, http.StatusCreated, memoryapi.PackageStageResponse{
			ResponseMetadata: memoryapi.Metadata(requestID), Stage: &stage, Preview: stage.Preview,
		})
	case strings.HasPrefix(r.URL.Path, memoryapi.PackageStagePath+"/"):
		s.handleMemoryPackageStage(w, r, requestID, ctx, service)
	case strings.HasPrefix(r.URL.Path, memoryapi.PackageExportPrefix+"/"):
		s.exportMemoryPackage(w, r, requestID, ctx, service)
	default:
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
	}
}

func (s *Server) handleMemoryPackageStage(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.PackageStagePath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		return
	}
	stageID := strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		if parts[1] != "activate" {
			s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
			return
		}
		if r.Method != http.MethodPost {
			s.writeMemoryPackageMethodError(w, requestID, http.MethodPost)
			return
		}
		result, err := service.ActivateImport(ctx, stageID)
		if err != nil {
			s.writeMemoryServiceError(w, requestID, err)
			return
		}
		s.writeMemoryJSON(w, http.StatusOK, memoryapi.PackageActivationResponse{
			ResponseMetadata: memoryapi.Metadata(requestID), Result: result,
		})
		return
	}
	if r.Method != http.MethodDelete {
		s.writeMemoryPackageMethodError(w, requestID, http.MethodDelete)
		return
	}
	if err := service.DiscardImportStage(ctx, stageID); err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.PackageDiscardResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), StageID: stageID, Discarded: true,
	})
}

func (s *Server) exportMemoryPackage(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	if r.Method != http.MethodGet {
		s.writeMemoryPackageMethodError(w, requestID, http.MethodGet)
		return
	}
	includePersonal, err := parseMemoryPackageExportQuery(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	chunkID := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.PackageExportPrefix+"/"), "/")
	if chunkID == "" || strings.Contains(chunkID, "/") {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	var archive bytes.Buffer
	result, err := service.ExportPackage(ctx, &archive, memoryService.ExportPackageRequest{
		ChunkID: memory.ChunkID(chunkID), IncludePersonal: includePersonal,
	})
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	digest := sha256.Sum256(archive.Bytes())
	w.Header().Set("Content-Type", memoryapi.PackageMediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.Filename}))
	w.Header().Set("Content-Length", strconv.Itoa(archive.Len()))
	w.Header().Set("ETag", fmt.Sprintf(`"sha256-%x"`, digest))
	w.WriteHeader(http.StatusOK)
	_, _ = archive.WriteTo(w)
}

func parseMemoryPackageExportQuery(r *http.Request) (bool, error) {
	query := r.URL.Query()
	for name, values := range query {
		if name != "include_personal" || len(values) != 1 {
			return false, fmt.Errorf("unsupported package export query %q", name)
		}
	}
	raw := strings.TrimSpace(query.Get("include_personal"))
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

func (s *Server) readMemoryPackage(w http.ResponseWriter, r *http.Request, requestID string, service *memoryService.Service) (kpackage.ValidatedPackage, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != memoryapi.PackageMediaType && mediaType != "application/zip" && mediaType != "application/octet-stream") {
		s.writeMemoryError(w, requestID, http.StatusUnsupportedMediaType, memoryService.ErrorCodeInvalid, "The Memory package media type is not supported.")
		return kpackage.ValidatedPackage{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, kpackage.HardMaxArchiveBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeMemoryError(w, requestID, http.StatusRequestEntityTooLarge, memoryService.ErrorCodeInvalid, "The Memory package is too large.")
		} else {
			s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory package could not be read.")
		}
		return kpackage.ValidatedPackage{}, false
	}
	pkg, err := service.ValidateImportArchive(r.Context(), data)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return kpackage.ValidatedPackage{}, false
	}
	return pkg, true
}

func parseMemoryPackageStageQuery(r *http.Request) (memoryService.ImportConflictPolicy, bool, error) {
	query := r.URL.Query()
	for name, values := range query {
		if (name != "conflict_policy" && name != "review_approved") || len(values) != 1 {
			return "", false, fmt.Errorf("unsupported package stage query %q", name)
		}
	}
	policy := memoryService.ImportConflictPolicy(strings.TrimSpace(query.Get("conflict_policy")))
	if err := policy.Validate(); err != nil {
		return "", false, err
	}
	reviewApproved := false
	if raw := strings.TrimSpace(query.Get("review_approved")); raw != "" {
		var err error
		reviewApproved, err = strconv.ParseBool(raw)
		if err != nil {
			return "", false, err
		}
	}
	return policy, reviewApproved, nil
}

func (s *Server) writeMemoryPackageMethodError(w http.ResponseWriter, requestID string, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
}

func memoryServiceHTTPStatus(code memoryService.ErrorCode) int {
	switch code {
	case memoryService.ErrorCodeInvalid:
		return http.StatusBadRequest
	case memoryService.ErrorCodeForbidden:
		return http.StatusForbidden
	case memoryService.ErrorCodeNotFound:
		return http.StatusNotFound
	case memoryService.ErrorCodeConflict, memoryService.ErrorCodeDependency:
		return http.StatusConflict
	case memoryService.ErrorCodeStale:
		return http.StatusGone
	case memoryService.ErrorCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
