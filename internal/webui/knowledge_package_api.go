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

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgePackages(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}

	switch {
	case r.URL.Path == knowledgeapi.PackagePreviewPath:
		if r.Method != http.MethodPost {
			s.writeKnowledgePackageMethodError(w, requestID, http.MethodPost)
			return
		}
		if !s.requireNoKnowledgeQuery(w, r, requestID) {
			return
		}
		pkg, ok := s.readKnowledgePackage(w, r, requestID, service)
		if !ok {
			return
		}
		preview, err := service.PreviewImport(ctx, pkg)
		if err != nil {
			s.writeKnowledgeServiceError(w, requestID, err)
			return
		}
		s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.PackagePreviewResponse{
			ResponseMetadata: knowledgeapi.Metadata(requestID), Preview: preview,
		})
	case r.URL.Path == knowledgeapi.PackageStagePath:
		if r.Method != http.MethodPost {
			s.writeKnowledgePackageMethodError(w, requestID, http.MethodPost)
			return
		}
		policy, reviewApproved, err := parseKnowledgePackageStageQuery(r)
		if err != nil {
			s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
			return
		}
		pkg, ok := s.readKnowledgePackage(w, r, requestID, service)
		if !ok {
			return
		}
		stage, err := service.StageImport(ctx, knowledgeService.StageImportRequest{
			Package: pkg, ConflictPolicy: policy, ReviewApproved: reviewApproved,
		})
		if err != nil {
			classified := knowledgeService.ClassifyError(err)
			status := knowledgeServiceHTTPStatus(classified.Code)
			s.writeKnowledgeJSON(w, status, knowledgeapi.PackageStageResponse{
				ResponseMetadata: knowledgeapi.Metadata(requestID), Preview: stage.Preview, Error: classified,
			})
			return
		}
		w.Header().Set("Location", knowledgeapi.PackageStageItemPath(stage.ID))
		s.writeKnowledgeJSON(w, http.StatusCreated, knowledgeapi.PackageStageResponse{
			ResponseMetadata: knowledgeapi.Metadata(requestID), Stage: &stage, Preview: stage.Preview,
		})
	case strings.HasPrefix(r.URL.Path, knowledgeapi.PackageStagePath+"/"):
		s.handleKnowledgePackageStage(w, r, requestID, ctx, service)
	case strings.HasPrefix(r.URL.Path, knowledgeapi.PackageExportPrefix+"/"):
		s.exportKnowledgePackage(w, r, requestID, ctx, service)
	default:
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
	}
}

func (s *Server) handleKnowledgePackageStage(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	if !s.requireNoKnowledgeQuery(w, r, requestID) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.PackageStagePath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		return
	}
	stageID := strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		if parts[1] != "activate" {
			s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
			return
		}
		if r.Method != http.MethodPost {
			s.writeKnowledgePackageMethodError(w, requestID, http.MethodPost)
			return
		}
		result, err := service.ActivateImport(ctx, stageID)
		if err != nil {
			s.writeKnowledgeServiceError(w, requestID, err)
			return
		}
		s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.PackageActivationResponse{
			ResponseMetadata: knowledgeapi.Metadata(requestID), Result: result,
		})
		return
	}
	if r.Method != http.MethodDelete {
		s.writeKnowledgePackageMethodError(w, requestID, http.MethodDelete)
		return
	}
	if err := service.DiscardImportStage(ctx, stageID); err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.PackageDiscardResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), StageID: stageID, Discarded: true,
	})
}

func (s *Server) exportKnowledgePackage(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	if r.Method != http.MethodGet {
		s.writeKnowledgePackageMethodError(w, requestID, http.MethodGet)
		return
	}
	includePersonal, err := parseKnowledgePackageExportQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	chunkID := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.PackageExportPrefix+"/"), "/")
	if chunkID == "" || strings.Contains(chunkID, "/") {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	var archive bytes.Buffer
	result, err := service.ExportPackage(ctx, &archive, knowledgeService.ExportPackageRequest{
		ChunkID: knowledge.ChunkID(chunkID), IncludePersonal: includePersonal,
	})
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	digest := sha256.Sum256(archive.Bytes())
	w.Header().Set("Content-Type", knowledgeapi.PackageMediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.Filename}))
	w.Header().Set("Content-Length", strconv.Itoa(archive.Len()))
	w.Header().Set("ETag", fmt.Sprintf(`"sha256-%x"`, digest))
	w.WriteHeader(http.StatusOK)
	_, _ = archive.WriteTo(w)
}

func parseKnowledgePackageExportQuery(r *http.Request) (bool, error) {
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

func (s *Server) readKnowledgePackage(w http.ResponseWriter, r *http.Request, requestID string, service *knowledgeService.Service) (kpackage.ValidatedPackage, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != knowledgeapi.PackageMediaType && mediaType != "application/zip" && mediaType != "application/octet-stream") {
		s.writeKnowledgeError(w, requestID, http.StatusUnsupportedMediaType, knowledgeService.ErrorCodeInvalid, "The Knowledge package media type is not supported.")
		return kpackage.ValidatedPackage{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, kpackage.HardMaxArchiveBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeKnowledgeError(w, requestID, http.StatusRequestEntityTooLarge, knowledgeService.ErrorCodeInvalid, "The Knowledge package is too large.")
		} else {
			s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge package could not be read.")
		}
		return kpackage.ValidatedPackage{}, false
	}
	pkg, err := service.ValidateImportArchive(r.Context(), data)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return kpackage.ValidatedPackage{}, false
	}
	return pkg, true
}

func parseKnowledgePackageStageQuery(r *http.Request) (knowledgeService.ImportConflictPolicy, bool, error) {
	query := r.URL.Query()
	for name, values := range query {
		if (name != "conflict_policy" && name != "review_approved") || len(values) != 1 {
			return "", false, fmt.Errorf("unsupported package stage query %q", name)
		}
	}
	policy := knowledgeService.ImportConflictPolicy(strings.TrimSpace(query.Get("conflict_policy")))
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

func (s *Server) writeKnowledgePackageMethodError(w http.ResponseWriter, requestID string, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
}

func knowledgeServiceHTTPStatus(code knowledgeService.ErrorCode) int {
	switch code {
	case knowledgeService.ErrorCodeInvalid:
		return http.StatusBadRequest
	case knowledgeService.ErrorCodeForbidden:
		return http.StatusForbidden
	case knowledgeService.ErrorCodeNotFound:
		return http.StatusNotFound
	case knowledgeService.ErrorCodeConflict, knowledgeService.ErrorCodeDependency:
		return http.StatusConflict
	case knowledgeService.ErrorCodeStale:
		return http.StatusGone
	case knowledgeService.ErrorCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
