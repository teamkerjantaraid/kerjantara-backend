package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	minioClient *minio.Client
	bucketName  string
	isSupabase  bool
	s3Path      string
}

var GlobalClient *Client

func InitStorage(endpoint, accessKey, secretKey, bucketName string) (*Client, error) {
	useSSL := true
	isSupabase := false
	s3Path := ""

	if strings.HasPrefix(endpoint, "http://") {
		useSSL = false
		endpoint = strings.TrimPrefix(endpoint, "http://")
	} else if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
	}

	// Supabase S3 endpoint format: <ref>.supabase.co
	// Supabase S3 API lives at /storage/v1/s3 path prefix
	if strings.Contains(endpoint, ".supabase.co") {
		isSupabase = true
		s3Path = "/storage/v1/s3"
		// Strip any path suffix from endpoint, keep only host
		if idx := strings.Index(endpoint, "/"); idx != -1 {
			endpoint = endpoint[:idx]
		}
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	}

	// Inject custom transport for Supabase to prefix /storage/v1/s3
	if isSupabase {
		opts.Transport = &supabaseTransport{
			s3Path: s3Path,
			base:   http.DefaultTransport,
		}
	}

	minioClient, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize minio client: %w", err)
	}

	client := &Client{
		minioClient: minioClient,
		bucketName:  bucketName,
		isSupabase:  isSupabase,
		s3Path:      s3Path,
	}

	GlobalClient = client
	return client, nil
}

// supabaseTransport rewrites request URL paths to include Supabase S3 prefix.
type supabaseTransport struct {
	s3Path string
	base   http.RoundTripper
}

func (t *supabaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone request to avoid mutating the original
	r := req.Clone(req.Context())
	r.URL.Path = t.s3Path + r.URL.Path
	return t.base.RoundTrip(r)
}

func (c *Client) UploadFile(ctx context.Context, objectName string, reader io.Reader, objectSize int64, contentType string) error {
	_, err := c.minioClient.PutObject(ctx, c.bucketName, objectName, reader, objectSize, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload object %s: %w", objectName, err)
	}
	return nil
}

func (c *Client) GetSignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	reqParams := make(url.Values)
	presignedURL, err := c.minioClient.PresignedGetObject(ctx, c.bucketName, objectName, expiry, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL for %s: %w", objectName, err)
	}
	return presignedURL.String(), nil
}
