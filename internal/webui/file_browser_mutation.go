package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/lkarlslund/koder/internal/id"
)

const maxFileUploadBytes = 256 << 20

type fileMutationRequest struct {
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
}

// These are explicit human file-manager actions, like the existing viewer,
// not agent tool calls. OS permissions apply; os.Root confines every operation
// to the session project even if a path component is replaced with a symlink.
func (s *Server) handleSessionFileMutation(w http.ResponseWriter, r *http.Request, sessionID id.ID, action string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := http.NewCrossOriginProtection().Check(r); err != nil {
		http.Error(w, "cross-origin file changes are not allowed", http.StatusForbidden)
		return
	}
	// Require a non-simple request as well, including on HTTP where browsers
	// may omit Sec-Fetch-Site. Cross-origin forms cannot provide this header.
	if r.Header.Get("X-Koder-File-Action") != "1" {
		http.Error(w, "missing file action header", http.StatusForbidden)
		return
	}
	session, err := s.controller.SessionByID(r.Context(), sessionID)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	if strings.TrimSpace(session.ProjectRoot) == "" {
		http.Error(w, "session has no project root", http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(session.ProjectRoot)
	if err != nil {
		writeFileMutationError(w, err)
		return
	}
	defer func() { _ = root.Close() }()
	var req fileMutationRequest
	if action == "upload" {
		req.Path = r.URL.Query().Get("path")
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeFileMutationError(w, fmt.Errorf("decode file action: %w", err))
			return
		}
	}
	if err := validateFileMutationPath(req.Path); err != nil {
		writeFileMutationError(w, err)
		return
	}
	switch action {
	case "upload":
		if r.ContentLength > maxFileUploadBytes {
			http.Error(w, "maximum upload size is 256 MB per file", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFileUploadBytes)
		err = uploadProjectFile(root, req.Path, r.Body)
	case "mkdir":
		err = root.Mkdir(req.Path, 0o755)
	case "move":
		if err = validateFileMutationPath(req.Destination); err == nil {
			if req.Destination == req.Path || strings.HasPrefix(req.Destination, req.Path+"/") {
				err = errors.New("choose a different destination outside the source folder")
			} else {
				err = moveProjectFile(root, req.Path, req.Destination)
			}
		}
	case "delete":
		// Lstat and RemoveAll act on the link itself, never its target.
		if _, err = root.Lstat(req.Path); err == nil {
			if req.Recursive {
				err = root.RemoveAll(req.Path)
			} else {
				err = root.Remove(req.Path)
			}
		}
	}
	if err != nil {
		writeFileMutationError(w, err)
		return
	}
	writeFileBrowserJSON(w, r, map[string]string{"path": req.Path, "destination": req.Destination})
}

func validateFileMutationPath(name string) error {
	if name == "" || name == "." || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." {
		return errors.New("choose a relative path inside the project; the project root cannot be changed")
	}
	return nil
}

// Stage in the destination filesystem and publish with Link, which atomically
// refuses existing names. Failed/disconnected uploads never expose half a file.
func uploadProjectFile(root *os.Root, name string, body io.Reader) error {
	dir, err := root.OpenRoot(path.Dir(name))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Lstat(path.Base(name)); err == nil {
		return fmt.Errorf("upload %s: %w", name, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp := ".koder-upload-" + string(id.New())
	file, err := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(temp) }()
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("upload incomplete: %w", err)
	}
	return dir.Link(temp, path.Base(name))
}

func writeFileMutationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, os.ErrExist):
		status = http.StatusConflict
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, os.ErrPermission):
		status = http.StatusForbidden
	}
	http.Error(w, err.Error(), status)
}
