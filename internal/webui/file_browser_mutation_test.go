package webui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFileManagerMutations(t *testing.T) {
	dir := t.TempDir()
	ctrl := newTestControllerWithWorkdir(t, dir)
	state := selectedTestState(t, ctrl)
	s := &Server{controller: ctrl}
	request := func(action string, payload fileMutationRequest, body string) *httptest.ResponseRecorder {
		t.Helper()
		address := "/files/" + action
		if action == "upload" {
			address += "?path=" + url.QueryEscape(payload.Path)
		} else {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = string(data)
		}
		r := httptest.NewRequest(http.MethodPost, address, strings.NewReader(body))
		r.Header.Set("X-Koder-File-Action", "1")
		w := httptest.NewRecorder()
		s.handleSessionFilesAPI(w, r, state.Session.ID, []string{action})
		return w
	}
	assertStatus := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body.String())
		}
	}
	assertStatus(request("mkdir", fileMutationRequest{Path: "documents"}, ""), http.StatusOK)
	assertStatus(request("upload", fileMutationRequest{Path: "a file.txt"}, "hello\x00world"), http.StatusOK)
	assertStatus(request("upload", fileMutationRequest{Path: "a file.txt"}, "replacement"), http.StatusConflict)
	data, err := os.ReadFile(filepath.Join(dir, "a file.txt"))
	if err != nil || string(data) != "hello\x00world" {
		t.Fatalf("collision changed file: %q %v", data, err)
	}
	assertStatus(request("move", fileMutationRequest{Path: "a file.txt", Destination: "documents/renamed.txt"}, ""), http.StatusOK)
	assertStatus(request("mkdir", fileMutationRequest{Path: "documents"}, ""), http.StatusConflict)
	assertStatus(request("upload", fileMutationRequest{Path: "documents/other.txt"}, "other"), http.StatusOK)
	assertStatus(request("move", fileMutationRequest{Path: "documents/renamed.txt", Destination: "documents/other.txt"}, ""), http.StatusConflict)
	assertStatus(request("move", fileMutationRequest{Path: "documents", Destination: "documents/nested"}, ""), http.StatusBadRequest)
	assertStatus(request("move", fileMutationRequest{Path: "documents", Destination: "moved"}, ""), http.StatusOK)
	assertStatus(request("delete", fileMutationRequest{Path: "moved"}, ""), http.StatusConflict)
	assertStatus(request("delete", fileMutationRequest{Path: "moved", Recursive: true}, ""), http.StatusOK)
	if _, err := os.Stat(filepath.Join(dir, "moved")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory not deleted: %v", err)
	}
	for _, action := range []string{"mkdir", "upload", "move", "delete"} {
		for _, bad := range []string{"", ".", "..", "../outside", "/", "/absolute", "a/../b", "a//b", "a\\b"} {
			assertStatus(request(action, fileMutationRequest{Path: bad, Destination: "safe", Recursive: true}, "data"), http.StatusBadRequest)
		}
	}
	assertStatus(request("upload", fileMutationRequest{Path: "keep.txt"}, "keep"), http.StatusOK)
	assertStatus(request("move", fileMutationRequest{Path: "keep.txt", Destination: "../escape"}, ""), http.StatusBadRequest)
	assertStatus(request("delete", fileMutationRequest{Path: "keep.txt"}, ""), http.StatusOK)
	assertStatus(request("delete", fileMutationRequest{Path: "keep.txt"}, ""), http.StatusNotFound)
	assertStatus(request("upload", fileMutationRequest{Path: "missing/file"}, "data"), http.StatusNotFound)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"upload", "mkdir", "move", "delete"} {
		w := request(action, fileMutationRequest{Path: "escape/keep.txt", Destination: "elsewhere", Recursive: true}, "replace")
		if w.Code == http.StatusOK {
			t.Fatalf("%s followed symlink outside project", action)
		}
	}
	assertStatus(request("upload", fileMutationRequest{Path: "source"}, "source"), http.StatusOK)
	if w := request("move", fileMutationRequest{Path: "source", Destination: "escape/new"}, ""); w.Code == http.StatusOK {
		t.Fatal("move escaped project")
	}
	// Deleting a symlink removes only the link, even with recursive requested.
	assertStatus(request("delete", fileMutationRequest{Path: "escape", Recursive: true}, ""), http.StatusOK)
	data, err = os.ReadFile(filepath.Join(outside, "keep.txt"))
	if err != nil || string(data) != "untouched" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}

	for _, tc := range []struct {
		name, method, origin, header string
		length                       int64
		want                         int
	}{
		{"get", "GET", "", "1", 0, 405},
		{"missing header", "POST", "", "", 0, 403},
		{"cross origin", "POST", "https://attacker.invalid", "1", 0, 403},
		{"oversized", "POST", "", "1", maxFileUploadBytes + 1, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/files/upload?path=test", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Koder-File-Action", tc.header)
			r.ContentLength = tc.length
			w := httptest.NewRecorder()
			s.handleSessionFilesAPI(w, r, state.Session.ID, []string{"upload"})
			assertStatus(w, tc.want)
		})
	}
}

func TestUploadCleanupAndConcurrentCollision(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	// An unknown-length request crossing the limit must clean up staging too.
	body := http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader("too large")), 3)
	if err := uploadProjectFile(root, "partial", body); err == nil {
		t.Fatal("accepted oversized streaming body")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial upload leaked: %v %v", entries, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		wg.Go(func() { results <- uploadProjectFile(root, "same", bytes.NewBufferString(content)) })
	}
	wg.Wait()
	close(results)
	success, collision := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, os.ErrExist) {
			collision++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || collision != 1 {
		t.Fatalf("success=%d collision=%d", success, collision)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "same" {
		t.Fatalf("staging leaked: %v %v", entries, err)
	}
}

func TestConcurrentFileMovesDoNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{"first", "second"} {
		if err := root.WriteFile(name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		wg.Go(func() { results <- moveProjectFile(root, name, "destination") })
	}
	wg.Wait()
	close(results)
	success, collision := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, os.ErrExist) {
			collision++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || collision != 1 {
		t.Fatalf("success=%d collision=%d", success, collision)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("move lost source data: %v %v", entries, err)
	}
}
