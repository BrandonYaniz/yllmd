package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
)

func TestDownloadResumesPartialFile(t *testing.T) {
	content := []byte(strings.Repeat("model-data-", 1024))
	var receivedRange string
	offset := len(content) / 3
	client := testClient(func(request *http.Request) *http.Response {
		receivedRange = request.Header.Get("Range")
		return testResponse(http.StatusPartialContent, content[offset:])
	})

	directory := t.TempDir()
	partial := filepath.Join(directory, "model.gguf.part")
	if err := os.WriteFile(partial, content[:offset], 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := testArtifact("http://models.test/model.gguf", content)
	path, err := (Downloader{Client: client, AllowHTTP: true}).Download(context.Background(), artifact, directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receivedRange != fmt.Sprintf("bytes=%d-", offset) {
		t.Fatalf("range = %q", receivedRange)
	}
	downloaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(content) {
		t.Fatal("downloaded content does not match")
	}
}

func TestDownloadRestartsWhenServerIgnoresRange(t *testing.T) {
	content := []byte("complete model")
	client := testClient(func(_ *http.Request) *http.Response { return testResponse(http.StatusOK, content) })
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "model.gguf.part"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := (Downloader{Client: client, AllowHTTP: true}).Download(context.Background(), testArtifact("http://models.test/model.gguf", content), directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _ := os.ReadFile(path)
	if string(downloaded) != string(content) {
		t.Fatalf("downloaded = %q", downloaded)
	}
}

func TestDownloadPreservesPartialOnShortResponse(t *testing.T) {
	content := []byte("expected complete model")
	client := testClient(func(_ *http.Request) *http.Response { return testResponse(http.StatusOK, content[:5]) })
	directory := t.TempDir()
	_, err := (Downloader{Client: client, AllowHTTP: true}).Download(context.Background(), testArtifact("http://models.test/model.gguf", content), directory, nil)
	if err == nil || !strings.Contains(err.Error(), "downloaded size") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "model.gguf.part")); err != nil {
		t.Fatalf("partial download was not preserved: %v", err)
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	content := []byte("model")
	client := testClient(func(_ *http.Request) *http.Response { return testResponse(http.StatusOK, content) })
	artifact := testArtifact("http://models.test/model.gguf", content)
	artifact.SHA256 = strings.Repeat("0", 64)
	_, err := (Downloader{Client: client, AllowHTTP: true}).Download(context.Background(), artifact, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadRejectsUnsafeFilename(t *testing.T) {
	artifact := testArtifact("https://example.com/model", []byte("model"))
	artifact.Filename = "../model.gguf"
	_, err := (Downloader{}).Download(context.Background(), artifact, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadCompletesAlreadyDownloadedPartial(t *testing.T) {
	content := []byte("complete model")
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "model.gguf.part"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	client := testClient(func(_ *http.Request) *http.Response {
		t.Fatal("completed partial should not make a request")
		return nil
	})
	path, err := (Downloader{Client: client}).Download(context.Background(), testArtifact("https://models.test/model.gguf", content), directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(directory, "model.gguf") {
		t.Fatalf("path = %q", path)
	}
}

func testArtifact(url string, content []byte) catalog.Artifact {
	digest := sha256.Sum256(content)
	return catalog.Artifact{
		URL:       url,
		Filename:  "model.gguf",
		SizeBytes: uint64(len(content)),
		SHA256:    hex.EncodeToString(digest[:]),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testClient(respond func(*http.Request) *http.Response) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return respond(request), nil
	})}
}

func testResponse(status int, content []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(content))),
	}
}
