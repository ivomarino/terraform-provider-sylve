package sylveclient

import (
	"context"
	"fmt"
	"time"
)

// Download is Sylve's representation of a fetched file -- an ISO, a
// cloud image, or a jail base rootfs archive. All three go through the
// same mechanism: POST /api/utilities/downloads with a "url" that is
// auto-classified by shape (a magnet URI -> torrent, a valid URL ->
// http, an absolute filesystem path -> "path", meaning "copy/reference
// an already-present local file" -- no network fetch at all). A VM's
// `iso` attribute (not yet wired into this provider's sylve_vm resource)
// references one of these by UUID.
type Download struct {
	ID                     int    `json:"id"`
	UUID                   string `json:"uuid"`
	Path                   string `json:"path"`
	Name                   string `json:"name"`
	Type                   string `json:"type"` // "http", "torrent", or "path" -- server-detected, not settable
	URL                    string `json:"url"`
	Progress               int    `json:"progress"`
	Size                   int64  `json:"size"`
	UType                  string `json:"uType"` // "base-rootfs", "cloud-init", or "uncategoried"
	Error                  string `json:"error"`
	AutomaticExtraction    bool   `json:"automaticExtraction"`
	AutomaticRawConversion bool   `json:"automaticRawConversion"`
	IgnoreTLS              bool   `json:"ignoreTLS"`
	ExtractedPath          string `json:"extractedPath"`
	Status                 string `json:"status"` // "pending", "processing", "done", or "failed"
}

type downloadListEnvelope struct {
	Data []Download `json:"data"`
}

type createDownloadRequest struct {
	URL                    string `json:"url"`
	Filename               string `json:"filename,omitempty"`
	IgnoreTLS              bool   `json:"ignoreTLS"`
	AutomaticExtraction    bool   `json:"automaticExtraction"`
	AutomaticRawConversion bool   `json:"automaticRawConversion"`
	DownloadType           string `json:"downloadType"`
}

// CreateDownload starts a download (POST /api/utilities/downloads,
// "file_download_started" -- fire and forget, processed by a background
// job queue regardless of type, including "path"). The response carries
// no id/uuid, so use WaitForDownload afterwards to both learn the
// assigned UUID and block until it reaches a terminal status, same
// pattern as CreateVM/CreateManualSwitch needing a follow-up lookup.
func (c *Client) CreateDownload(ctx context.Context, url, filename, uType string, ignoreTLS, autoExtract, autoRawConvert bool) error {
	body := createDownloadRequest{
		URL:                    url,
		Filename:               filename,
		IgnoreTLS:              ignoreTLS,
		AutomaticExtraction:    autoExtract,
		AutomaticRawConversion: autoRawConvert,
		DownloadType:           uType,
	}
	if err := c.do(ctx, "POST", "/api/utilities/downloads", body, nil); err != nil {
		return fmt.Errorf("starting download %q: %w", url, err)
	}
	return nil
}

// ListDownloads returns every download on the node.
func (c *Client) ListDownloads(ctx context.Context) ([]Download, error) {
	var out downloadListEnvelope
	if err := c.do(ctx, "GET", "/api/utilities/downloads", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetDownloadByID finds a download by its numeric ID. There is no
// per-ID GET endpoint (only the combined list, plus a
// signed-URL-download endpoint that does something unrelated despite
// the similar path shape), so this lists and filters. Returns an error
// satisfying IsNotFound if no download has that ID.
func (c *Client) GetDownloadByID(ctx context.Context, id int) (*Download, error) {
	downloads, err := c.ListDownloads(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range downloads {
		if d.ID == id {
			return &d, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("download id %d not found", id)}
}

// GetDownloadByURL finds a download by its stored URL -- Sylve enforces
// URL uniqueness (create 400s with url_already_exists otherwise), so
// this is a safe lookup key right after CreateDownload. Note that for a
// "path"-type download the stored URL is the cleaned source path, not
// necessarily byte-identical to what was requested (see the source's own
// path.Clean handling) -- GetDownloadByURL compares against the exact
// value the caller passed, which matches in the common case but can miss
// if the input path had redundant separators etc.
func (c *Client) GetDownloadByURL(ctx context.Context, url string) (*Download, error) {
	downloads, err := c.ListDownloads(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range downloads {
		if d.URL == url {
			return &d, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("download %q not found after create", url)}
}

// DeleteDownload removes a download by numeric ID. DELETE
// /api/utilities/downloads/{id}.
func (c *Client) DeleteDownload(ctx context.Context, id int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/utilities/downloads/%d", id), nil, nil)
}

// WaitForDownload polls GetDownloadByURL until the download reaches
// status "done" (returns it) or "failed" (returns an error), or timeout
// elapses. Downloading a real multi-GB cloud image or ISO over the
// network can legitimately take a long time -- callers (this provider's
// Create) should set timeout generously; there is no way to make this
// asynchronous within a single `terraform apply` today.
func (c *Client) WaitForDownload(ctx context.Context, url string, timeout time.Duration) (*Download, error) {
	deadline := time.Now().Add(timeout)
	for {
		d, err := c.GetDownloadByURL(ctx, url)
		if err != nil {
			return nil, err
		}
		switch d.Status {
		case "done":
			return d, nil
		case "failed":
			return nil, fmt.Errorf("download %q failed: %s", url, d.Error)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("download %q did not finish within %s (last status %q, progress %d%%)",
				url, timeout, d.Status, d.Progress)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
