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

func TestKnowledgeGraphVendorAssetsArePinnedAndLocal(t *testing.T) {
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
			path: "assets/vendor/knowledge-layouts/knowledge-layouts.min.js", sha256: "5e9357010a063bdbf8d26eb963108140f5729667ce00466036ea071bccf71d90",
			globalName: ".KoderKnowledgeLayouts=",
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
			defer result.Body.Close()
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
		"assets/vendor/knowledge-layouts/LICENSE-forceatlas2.txt",
		"assets/vendor/knowledge-layouts/LICENSE-graphology-utils.txt",
		"assets/vendor/knowledge-layouts/LICENSE-noverlap.txt",
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

func TestKnowledgeGraphVendorJavaScriptCompatibility(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/knowledge_vendor_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("knowledge vendor JavaScript test: %v\n%s", err, output)
	}
}
