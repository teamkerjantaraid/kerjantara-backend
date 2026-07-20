// Package storage provides file upload and signed URL generation
// using Supabase Storage REST API or MinIO (for local dev).
//
// For Supabase: set STORAGE_BACKEND=supabase (or leave STORAGE_ENDPOINT empty).
// For MinIO local: set STORAGE_ENDPOINT=http://localhost:9000.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client abstracts over Supabase Storage REST API and MinIO/S3.
type Client struct {
	// Supabase mode
	supabaseURL    string // e.g. https://xjf.supabase.co
	supabaseKey    string // service role key
	bucketName     string
	httpClient     *http.Client

	// MinIO mode
	minioClient *minio.Client

	backend string // "supabase" | "minio"
}

var GlobalClient *Client

// InitStorage initialises the storage client.
//
// Supabase mode  – pass supabaseURL (https://<ref>.supabase.co) and serviceKey.
//                  endpoint should be empty or equal to supabaseURL.
// MinIO mode     – pass endpoint (http://localhost:9000) and minio credentials.
func InitStorage(endpoint, accessKey, secretKey, bucketName string) (*Client, error) {
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey  := os.Getenv("SUPABASE_SERVICE_KEY")

	// Detect Supabase: either endpoint contains supabase.co OR no endpoint but SUPABASE_URL is set.
	isSupabase := strings.Contains(endpoint, "supabase.co") ||
		(endpoint == "" && supabaseURL != "")

	if isSupabase {
		// Normalise base URL — strip trailing slash and any /storage/v1/s3 suffix.
		base := supabaseURL
		if base == "" {
			// Reconstruct from endpoint by stripping the path component.
			base = endpoint
			if idx := strings.Index(base, "/storage"); idx != -1 {
				base = base[:idx]
			}
			// Re-add scheme if stripped.
			if !strings.HasPrefix(base, "http") {
				base = "https://" + base
			}
		}
		base = strings.TrimRight(base, "/")

		// Use passed secretKey as service key if env not set.
		if supabaseKey == "" {
			supabaseKey = secretKey
		}

		c := &Client{
			supabaseURL: base,
			supabaseKey: supabaseKey,
			bucketName:  bucketName,
			httpClient:  &http.Client{Timeout: 60 * time.Second},
			backend:     "supabase",
		}
		GlobalClient = c
		return c, nil
	}

	// MinIO / generic S3 mode.
	useSSL := true
	ep := endpoint
	if strings.HasPrefix(ep, "http://") {
		useSSL = false
		ep = strings.TrimPrefix(ep, "http://")
	} else if strings.HasPrefix(ep, "https://") {
		ep = strings.TrimPrefix(ep, "https://")
	}

	region := os.Getenv("STORAGE_REGION")

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	}

	mc, err := minio.New(ep, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize minio client: %w", err)
	}

	c := &Client{
		minioClient: mc,
		bucketName:  bucketName,
		backend:     "minio",
	}
	GlobalClient = c
	return c, nil
}

// UploadFile uploads a file to storage.
func (c *Client) UploadFile(ctx context.Context, objectName string, reader io.Reader, objectSize int64, contentType string) error {
	if c.backend == "supabase" {
		return c.supabaseUpload(ctx, objectName, reader, contentType)
	}
	_, err := c.minioClient.PutObject(ctx, c.bucketName, objectName, reader, objectSize, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload object %s: %w", objectName, err)
	}
	return nil
}

// GetSignedURL returns a temporary URL to access a private file.
func (c *Client) GetSignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	if c.backend == "supabase" {
		return c.supabaseSignedURL(ctx, objectName, expiry)
	}
	reqParams := make(url.Values)
	presignedURL, err := c.minioClient.PresignedGetObject(ctx, c.bucketName, objectName, expiry, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL for %s: %w", objectName, err)
	}
	return presignedURL.String(), nil
}

// supabaseUpload uploads via Supabase Storage REST API.
// POST /storage/v1/object/<bucket>/<path>
func (c *Client) supabaseUpload(ctx context.Context, objectName string, reader io.Reader, contentType string) error {
	apiURL := fmt.Sprintf("%s/storage/v1/object/%s/%s", c.supabaseURL, c.bucketName, objectName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, reader)
	if err != nil {
		return fmt.Errorf("failed to build upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.supabaseKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true") // overwrite if exists

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// supabaseSignedURL creates a signed URL via Supabase Storage REST API.
// POST /storage/v1/object/sign/<bucket>/<path>
func (c *Client) supabaseSignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	apiURL := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", c.supabaseURL, c.bucketName, objectName)

	payload, _ := json.Marshal(map[string]int{
		"expiresIn": int(expiry.Seconds()),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to build sign request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.supabaseKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sign request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sign URL failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse sign response: %w", err)
	}
	if result.SignedURL == "" {
		return "", fmt.Errorf("signed URL empty in response")
	}

	// SignedURL from Supabase is a relative path like /storage/v1/object/sign/...
	// Prepend the base URL if needed.
	if strings.HasPrefix(result.SignedURL, "/") {
		return c.supabaseURL + result.SignedURL, nil
	}
	return result.SignedURL, nil
}
