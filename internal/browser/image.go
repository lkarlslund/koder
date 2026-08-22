package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/browserapi"
)

type imageSource struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Data string `json:"data"`
}

// Image returns image content rather than a rendering of the surrounding page.
func (m *Manager) Image(ctx context.Context, chat browserapi.Chat, locator browserapi.Locator) (browserapi.Binary, error) {
	tab, tabCtx, err := m.ownedSelected(ctx, chat)
	if err != nil {
		return browserapi.Binary{}, err
	}
	tab.mu.Lock()
	defer tab.mu.Unlock()

	var source imageSource
	opCtx, cancel := m.operationContext(ctx, tabCtx)
	defer cancel()
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := chromedp.Run(opCtx, chromedp.Evaluate(imageSourceExpression(locator), &source, awaitPromise)); err != nil {
		return browserapi.Binary{}, fmt.Errorf("resolve browser image: %w", err)
	}

	var binary browserapi.Binary
	switch source.Kind {
	case "data":
		binary, err = decodeDataImage(source.Data)
	case "resource":
		err = chromedp.Run(opCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var loadErr error
			binary, loadErr = m.loadedImage(ctx, tab, source.URL)
			return loadErr
		}))
	default:
		err = fmt.Errorf("unsupported browser image source %q", source.Kind)
	}
	if err != nil {
		return browserapi.Binary{}, err
	}
	if len(binary.Data) > maxBinarySize {
		return browserapi.Binary{}, errors.New("browser image exceeds 25 MiB")
	}
	if !strings.HasPrefix(binary.MIME, "image/") {
		return browserapi.Binary{}, fmt.Errorf("browser image resource has non-image content type %q", binary.MIME)
	}
	return binary, nil
}

func (m *Manager) loadedImage(ctx context.Context, tab *ownedTab, rawURL string) (browserapi.Binary, error) {
	if strings.HasPrefix(rawURL, "data:") {
		return decodeDataImage(rawURL)
	}
	if strings.HasPrefix(rawURL, "blob:") {
		return browserapi.Binary{}, errors.New("browser image blob could not be read directly")
	}

	var requestID network.RequestID
	mimeType := ""
	tab.dataMu.Lock()
	for index := len(tab.order) - 1; index >= 0; index-- {
		request := tab.requests[tab.order[index]]
		if request != nil && request.record.URL == rawURL && request.record.Finished {
			requestID = request.cdpID
			mimeType = request.record.MIME
			break
		}
	}
	tab.dataMu.Unlock()

	var data []byte
	if requestID != "" {
		data, _ = network.GetResponseBody(requestID).Do(ctx)
	}
	if len(data) == 0 {
		tree, err := page.GetResourceTree().Do(ctx)
		if err != nil {
			return browserapi.Binary{}, fmt.Errorf("inspect browser image resource: %w", err)
		}
		frameID, resourceMIME, ok := findFrameResource(tree, rawURL)
		if !ok {
			return browserapi.Binary{}, errors.New("browser image resource is no longer available in the page cache")
		}
		data, err = page.GetResourceContent(frameID, rawURL).Do(ctx)
		if err != nil {
			return browserapi.Binary{}, fmt.Errorf("read browser image resource: %w", err)
		}
		if mimeType == "" {
			mimeType = resourceMIME
		}
	}
	return imageBinary(rawURL, mimeType, data), nil
}

func findFrameResource(tree *page.FrameResourceTree, rawURL string) (frameID cdp.FrameID, mimeType string, ok bool) {
	if tree == nil || tree.Frame == nil {
		return "", "", false
	}
	for _, resource := range tree.Resources {
		if resource.URL == rawURL && !resource.Failed && !resource.Canceled {
			return tree.Frame.ID, resource.MimeType, true
		}
	}
	for _, child := range tree.ChildFrames {
		if frameID, mimeType, ok := findFrameResource(child, rawURL); ok {
			return frameID, mimeType, true
		}
	}
	return "", "", false
}

func decodeDataImage(raw string) (browserapi.Binary, error) {
	if !strings.HasPrefix(raw, "data:") {
		return browserapi.Binary{}, errors.New("browser image data URL is invalid")
	}
	header, payload, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return browserapi.Binary{}, errors.New("browser image data URL has no payload")
	}
	base64Encoded := false
	parts := strings.Split(header, ";")
	mimeType := parts[0]
	if mimeType == "" {
		mimeType = "text/plain"
	}
	for _, part := range parts[1:] {
		if strings.EqualFold(part, "base64") {
			base64Encoded = true
		}
	}
	var data []byte
	var err error
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return browserapi.Binary{}, fmt.Errorf("decode browser image data URL: %w", err)
	}
	return imageBinary("", mimeType, data), nil
}

func imageBinary(rawURL, mimeType string, data []byte) browserapi.Binary {
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	name := "browser-image" + imageExtension(mimeType)
	if parsed, err := url.Parse(rawURL); err == nil {
		if candidate := path.Base(parsed.Path); candidate != "" && candidate != "." && candidate != "/" {
			name = candidate
		}
	}
	return browserapi.Binary{Name: name, MIME: mimeType, Data: data}
}

func imageExtension(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/avif":
		return ".avif"
	default:
		return ""
	}
}

func imageSourceExpression(locator browserapi.Locator) string {
	return fmt.Sprintf(`(async()=>{%s;const e=resolve(%s,'capture');if(e instanceof HTMLImageElement){if(!e.complete||e.naturalWidth===0)throw new Error('Browser image has not loaded');const source=e.currentSrc||e.src;if(source.startsWith('blob:')){const blob=await (await fetch(source)).blob();const data=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=()=>reject(reader.error);reader.readAsDataURL(blob)});return {kind:'data',data}}return {kind:'resource',url:source}}if(e instanceof HTMLCanvasElement){return {kind:'data',data:e.toDataURL('image/png')}}const svg=e instanceof SVGSVGElement?e:e.closest?.('svg');if(svg){let markup=new XMLSerializer().serializeToString(svg);if(!markup.includes('xmlns='))markup=markup.replace('<svg','<svg xmlns="http://www.w3.org/2000/svg"');return {kind:'data',data:'data:image/svg+xml;charset=utf-8,'+encodeURIComponent(markup)}}throw new Error('Browser target is not an img, canvas, or inline SVG; use browser_capture with action=screenshot to capture rendered page elements')})()`, semanticResolverJS, mustJSON(locator))
}
