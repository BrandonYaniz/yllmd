package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/catalog"
)

type Progress struct {
	DownloadedBytes uint64 `json:"downloaded_bytes"`
	TotalBytes      uint64 `json:"total_bytes"`
}

type Downloader struct {
	Client    *http.Client
	AllowHTTP bool
}

func (d Downloader) Download(ctx context.Context, artifact catalog.Artifact, destinationDir string, report func(Progress)) (string, error) {
	if err := validateDownload(artifact, destinationDir, d.AllowHTTP); err != nil {
		return "", err
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	clientCopy := *client
	previousRedirectPolicy := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" && !(d.AllowHTTP && request.URL.Scheme == "http") {
			return fmt.Errorf("download redirect must use HTTPS")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	client = &clientCopy
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	finalPath := filepath.Join(destinationDir, artifact.Filename)
	if info, err := os.Stat(finalPath); err == nil {
		if uint64(info.Size()) == artifact.SizeBytes {
			if err := verifyFile(finalPath, artifact.SHA256); err == nil {
				reportProgress(report, artifact.SizeBytes, artifact.SizeBytes)
				return finalPath, nil
			}
		}
		return "", fmt.Errorf("existing download at %s does not match the catalog artifact", finalPath)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	partialPath := finalPath + ".part"
	offset, err := partialSize(partialPath, artifact.SizeBytes)
	if err != nil {
		return "", err
	}
	if offset == artifact.SizeBytes {
		if err := verifyFile(partialPath, artifact.SHA256); err != nil {
			return "", err
		}
		if err := os.Rename(partialPath, finalPath); err != nil {
			return "", fmt.Errorf("complete download: %w", err)
		}
		reportProgress(report, artifact.SizeBytes, artifact.SizeBytes)
		return finalPath, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", artifact.Filename, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("download %s: server returned %s", artifact.Filename, response.Status)
	}
	if offset > 0 && response.StatusCode == http.StatusOK {
		offset = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(partialPath, flags, 0o600)
	if err != nil {
		return "", err
	}
	reporter := &progressWriter{writer: file, downloaded: offset, total: artifact.SizeBytes, report: report, lastReport: time.Now()}
	remaining := artifact.SizeBytes - offset
	_, copyErr := io.Copy(reporter, io.LimitReader(response.Body, int64(remaining)+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("download %s: %w", artifact.Filename, copyErr)
	}
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	info, err := os.Stat(partialPath)
	if err != nil {
		return "", err
	}
	if uint64(info.Size()) != artifact.SizeBytes {
		return "", fmt.Errorf("downloaded size for %s is %d; expected %d", artifact.Filename, info.Size(), artifact.SizeBytes)
	}
	if err := verifyFile(partialPath, artifact.SHA256); err != nil {
		return "", err
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		return "", fmt.Errorf("complete download: %w", err)
	}
	reportProgress(report, artifact.SizeBytes, artifact.SizeBytes)
	return finalPath, nil
}

func validateDownload(artifact catalog.Artifact, destinationDir string, allowHTTP bool) error {
	if destinationDir == "" {
		return fmt.Errorf("download destination is required")
	}
	if artifact.Filename == "" || artifact.Filename != filepath.Base(artifact.Filename) || artifact.Filename == "." {
		return fmt.Errorf("artifact filename %q is unsafe", artifact.Filename)
	}
	parsed, err := url.Parse(artifact.URL)
	if err != nil {
		return fmt.Errorf("parse artifact URL: %w", err)
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return fmt.Errorf("artifact URL must use HTTPS")
	}
	if parsed.Host == "" {
		return fmt.Errorf("artifact URL host is required")
	}
	if artifact.SizeBytes == 0 {
		return fmt.Errorf("artifact size must be positive")
	}
	if artifact.SizeBytes > uint64(1<<63-2) {
		return fmt.Errorf("artifact size is too large")
	}
	checksum, err := hex.DecodeString(strings.TrimSpace(artifact.SHA256))
	if err != nil || len(checksum) != sha256.Size {
		return fmt.Errorf("artifact SHA-256 is invalid")
	}
	return nil
}

func partialSize(path string, expected uint64) (uint64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if uint64(info.Size()) > expected {
		if err := os.Remove(path); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return uint64(info.Size()), nil
}

func verifyFile(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("SHA-256 mismatch for %s: got %s", filepath.Base(path), actual)
	}
	return nil
}

type progressWriter struct {
	writer     io.Writer
	downloaded uint64
	total      uint64
	report     func(Progress)
	lastReport time.Time
}

func (w *progressWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.downloaded += uint64(n)
	if time.Since(w.lastReport) >= 250*time.Millisecond || w.downloaded == w.total {
		reportProgress(w.report, w.downloaded, w.total)
		w.lastReport = time.Now()
	}
	return n, err
}

func reportProgress(report func(Progress), downloaded, total uint64) {
	if report != nil {
		report(Progress{DownloadedBytes: downloaded, TotalBytes: total})
	}
}
