// Package gcs is a small Cloud Storage JSON API client, used by the cache backend
// and by the run-output exporter.
//
// It is deliberately thin: those two features need upload, download and delete of
// small objects, and the official client library would pull in a large dependency
// tree (gRPC, googleapis, protobuf) to do it. Authentication uses Application
// Default Credentials, so on Cloud Run — or wherever GOOGLE_APPLICATION_CREDENTIALS
// is set — it needs no key material in the binary.
package gcs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// DefaultBaseURL is the Cloud Storage JSON API endpoint.
	DefaultBaseURL = "https://storage.googleapis.com"
	// ReadWriteScope is the narrowest scope that allows object create/delete.
	ReadWriteScope = "https://www.googleapis.com/auth/devstorage.read_write"
	// defaultTimeout bounds a single object operation.
	defaultTimeout = 30 * time.Second
	// maxObjectBytes caps how much of an object is read into memory.
	maxObjectBytes = 64 << 20
)

// Client performs object operations against one storage endpoint. The endpoint is
// a field so tests can point it at a local stub server.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient builds a client authenticated with Application Default Credentials.
// The scope decides whether the client may only read or also write.
func NewClient(ctx context.Context, scope string) (*Client, error) {
	if strings.TrimSpace(scope) == "" {
		scope = ReadWriteScope
	}
	credentials, err := google.FindDefaultCredentials(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("load Google credentials (set GOOGLE_APPLICATION_CREDENTIALS to a service-account file, or run with an ambient identity): %w", err)
	}
	httpClient := oauth2.NewClient(ctx, credentials.TokenSource)
	httpClient.Timeout = defaultTimeout
	return NewClientWithHTTPClient(httpClient, DefaultBaseURL), nil
}

// NewClientWithHTTPClient wires a client around an existing HTTP client. Tests use
// it with an httptest server's client; production code uses NewClient.
func NewClientWithHTTPClient(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/")}
}

// Upload writes data as an object, replacing any object of the same name.
func (c *Client) Upload(ctx context.Context, bucket, object, contentType string, data []byte) error {
	endpoint := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		c.baseURL, url.PathEscape(bucket), url.QueryEscape(object))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	request.Header.Set("Content-Type", contentType)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload %s/%s: %w", bucket, object, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upload %s/%s: status %d (check that the bucket exists and the identity may write to it)",
			bucket, object, response.StatusCode)
	}
	return nil
}

// Download reads an object. It reports found=false for a missing object, which is
// a normal outcome for a cache lookup, and an error for anything else.
func (c *Client) Download(ctx context.Context, bucket, object string) ([]byte, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(bucket, object)+"?alt=media", nil)
	if err != nil {
		return nil, false, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("download %s/%s: %w", bucket, object, err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil
	case http.StatusForbidden:
		return nil, false, fmt.Errorf("download %s/%s: status %d (the identity may not read this bucket)", bucket, object, response.StatusCode)
	default:
		return nil, false, fmt.Errorf("download %s/%s: status %d", bucket, object, response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxObjectBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("read %s/%s: %w", bucket, object, err)
	}
	if len(data) > maxObjectBytes {
		return nil, false, fmt.Errorf("object %s/%s exceeds the %d byte limit", bucket, object, maxObjectBytes)
	}
	return data, true, nil
}

// Delete removes an object. Deleting an absent object is not an error.
func (c *Client) Delete(ctx context.Context, bucket, object string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.objectURL(bucket, object), nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", bucket, object, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete %s/%s: status %d", bucket, object, response.StatusCode)
	}
	return nil
}

// DeleteQuietly removes an object, logging instead of returning a failure. It is
// for cleanup paths where the caller cannot act on the error.
func (c *Client) DeleteQuietly(ctx context.Context, bucket, object string) {
	if err := c.Delete(ctx, bucket, object); err != nil {
		log.Printf("[WARN] gcs cleanup of %s/%s failed: %v", bucket, object, err)
	}
}

// objectURL is the JSON API URL for one object. Object names may contain "/", so
// the name is escaped into a single path segment.
func (c *Client) objectURL(bucket, object string) string {
	return fmt.Sprintf("%s/storage/v1/b/%s/o/%s", c.baseURL, url.PathEscape(bucket), url.PathEscape(object))
}

// URI renders the canonical gs:// reference for an object.
func URI(bucket, object string) string {
	return "gs://" + bucket + "/" + object
}

// HTTPSURL renders a browser-openable URL for an object: it is what the console
// links to, and it works for any viewer who has read access to the object.
func HTTPSURL(bucket, object string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, strings.ReplaceAll(object, " ", "%20"))
}
