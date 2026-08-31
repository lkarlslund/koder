package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const MaxRemoteMediaBytes = 25 * 1024 * 1024
const MaxPresentedMediaItems = 20

type MediaInput struct {
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
}

type RemoteMedia struct {
	Data     []byte
	Name     string
	MIMEType string
	URL      string
}

func NormalizeMediaArgs(args map[string]string) (map[string]string, error) {
	pathValue := strings.TrimSpace(args["path"])
	itemsValue := strings.TrimSpace(args["items"])
	if pathValue != "" && itemsValue != "" {
		return nil, errors.New("media path and items cannot be combined")
	}
	out := map[string]string{}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
	}
	if pathValue != "" {
		path, err := NormalizePathOrHTTPURL(pathValue)
		if err != nil {
			return nil, err
		}
		out["path"] = path
		return out, nil
	}
	items, err := DecodeMediaInputs(itemsValue)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Path, err = NormalizePathOrHTTPURL(items[index].Path)
		if err != nil {
			return nil, fmt.Errorf("media item %d: %w", index+1, err)
		}
		items[index].Title = strings.TrimSpace(items[index].Title)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	out["items"] = string(encoded)
	return out, nil
}

func MediaInputs(args map[string]string) ([]MediaInput, error) {
	if pathValue := strings.TrimSpace(args["path"]); pathValue != "" {
		return []MediaInput{{Path: pathValue, Title: strings.TrimSpace(args["title"])}}, nil
	}
	return DecodeMediaInputs(args["items"])
}

func DecodeMediaInputs(value string) ([]MediaInput, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("media path or items are required")
	}
	var items []MediaInput
	if err := json.Unmarshal([]byte(value), &items); err != nil {
		return nil, fmt.Errorf("decode media items: %w", err)
	}
	if len(items) == 0 {
		return nil, errors.New("at least one media item is required")
	}
	if len(items) > MaxPresentedMediaItems {
		return nil, fmt.Errorf("media items exceed the limit of %d", MaxPresentedMediaItems)
	}
	for index, item := range items {
		if strings.TrimSpace(item.Path) == "" {
			return nil, fmt.Errorf("media item %d path is required", index+1)
		}
	}
	return items, nil
}

func NormalizePathOrHTTPURL(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("path or URL is empty")
	}
	parsed, err := url.Parse(input)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		if parsed.Host == "" {
			return "", errors.New("remote media URL requires a host")
		}
		return parsed.String(), nil
	}
	return NormalizePathInput(input), nil
}

func IsHTTPURL(input string) bool {
	parsed, err := url.Parse(strings.TrimSpace(input))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func FetchRemoteMedia(ctx context.Context, client *http.Client, rawURL string) (RemoteMedia, error) {
	if client == nil {
		client = &http.Client{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return RemoteMedia{}, err
	}
	request.Header.Set("User-Agent", "koder/1.0")
	response, err := client.Do(request)
	if err != nil {
		return RemoteMedia{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8*1024))
		return RemoteMedia{}, fmt.Errorf("fetch media status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if response.ContentLength > MaxRemoteMediaBytes {
		return RemoteMedia{}, fmt.Errorf("remote media exceeds %d MiB", MaxRemoteMediaBytes/(1024*1024))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxRemoteMediaBytes+1))
	if err != nil {
		return RemoteMedia{}, fmt.Errorf("read remote media: %w", err)
	}
	if len(data) == 0 {
		return RemoteMedia{}, errors.New("remote media is empty")
	}
	if len(data) > MaxRemoteMediaBytes {
		return RemoteMedia{}, fmt.Errorf("remote media exceeds %d MiB", MaxRemoteMediaBytes/(1024*1024))
	}
	finalURL := rawURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	name := "remote-media"
	if _, params, parseErr := mime.ParseMediaType(response.Header.Get("Content-Disposition")); parseErr == nil {
		if filename := strings.TrimSpace(path.Base(params["filename"])); filename != "" && filename != "." && filename != "/" {
			name = filename
		}
	}
	if name == "remote-media" {
		if parsed, parseErr := url.Parse(finalURL); parseErr == nil {
			if candidate := strings.TrimSpace(path.Base(parsed.Path)); candidate != "" && candidate != "." && candidate != "/" {
				name = candidate
			}
		}
	}
	return RemoteMedia{Data: data, Name: name, MIMEType: normalizedMIME(response.Header.Get("Content-Type")), URL: finalURL}, nil
}

func normalizedMIME(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value
}
