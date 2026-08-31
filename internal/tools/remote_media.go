package tools

import (
	"context"
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

type RemoteMedia struct {
	Data     []byte
	Name     string
	MIMEType string
	URL      string
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
