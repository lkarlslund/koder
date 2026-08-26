package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestMemoryGraphVendorAssetsArePinnedAndLocal(t *testing.T) {
	t.Parallel()
	assets := []struct {
		path       string
		sha256     string
		globalName string
	}{
		{
			path: "assets/vendor/graphology/graphology.umd.min.js", sha256: "dc337efa23903f61e064c8e7e7f93a429e6855dccfc2458802b4ed30c621c087",
			globalName: ".graphology=",
		},
		{
			path: "assets/vendor/sigma/sigma.min.js", sha256: "58e30383ab428f832068d9d16a5215c65ba12430d438ed091c5703f398de9e16",
			globalName: ".Sigma=",
		},
		{
			path: "assets/vendor/memory-layouts/memory-layouts.min.js", sha256: "dedf3d87b9287d3f9f7d71deb476ae9b666fa20914caaaebc94eb2cc5a47f3ce",
			globalName: ".KoderMemoryLayouts=",
		},
	}
	for _, asset := range assets {
		asset := asset
		t.Run(asset.path, func(t *testing.T) {
			data, err := webAssets.ReadFile(asset.path)
			if err != nil {
				t.Fatalf("read embedded vendor asset: %v", err)
			}
			digest := sha256.Sum256(data)
			if got := hex.EncodeToString(digest[:]); got != asset.sha256 {
				t.Fatalf("vendor SHA-256 = %s, want %s", got, asset.sha256)
			}
			source := string(data)
			if !strings.Contains(source, asset.globalName) {
				t.Fatalf("vendor asset does not publish browser global %q", asset.globalName)
			}
			// Sigma can fetch an explicitly configured node image and Graphology has an
			// import method. Neither is an implicit dependency load. Reject embedded
			// remote locations instead: the explorer only points script tags at these
			// locally served UMD files.
			for _, forbidden := range []string{"http://", "https://", "//cdn.", "//unpkg.", "//esm.sh", "sourceMappingURL=http"} {
				if strings.Contains(source, forbidden) {
					t.Fatalf("vendor asset contains external loading primitive %q", forbidden)
				}
			}

			request := httptest.NewRequest(http.MethodGet, "/"+asset.path, nil)
			response := httptest.NewRecorder()
			assetHandler().ServeHTTP(response, request)
			result := response.Result()
			defer closeMemoryHTTPResponse(t, result)
			if result.StatusCode != http.StatusOK {
				t.Fatalf("GET local vendor asset status = %d", result.StatusCode)
			}
			served, err := io.ReadAll(result.Body)
			if err != nil {
				t.Fatalf("read served vendor asset: %v", err)
			}
			servedDigest := sha256.Sum256(served)
			if servedDigest != digest {
				t.Fatal("served vendor asset differs from embedded asset")
			}
		})
	}
	for _, license := range []string{
		"assets/vendor/graphology/LICENSE.txt",
		"assets/vendor/memory-layouts/LICENSE-forceatlas2.txt",
		"assets/vendor/memory-layouts/LICENSE-graphology-utils.txt",
		"assets/vendor/memory-layouts/LICENSE-noverlap.txt",
		"assets/vendor/sigma/LICENSE.txt",
	} {
		data, err := webAssets.ReadFile(license)
		if err != nil {
			t.Fatalf("read embedded license %q: %v", license, err)
		}
		if !strings.Contains(string(data), "Permission is hereby granted, free of charge") {
			t.Fatalf("vendor license %q is not the expected MIT text", license)
		}
	}
}

func TestMemoryGraphVendorJavaScriptCompatibility(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/memory_vendor_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("memory vendor JavaScript test: %v\n%s", err, output)
	}
}
