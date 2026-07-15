package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	minioClient *minio.Client
	bucketName  string
}

var GlobalClient *Client

func InitStorage(endpoint, accessKey, secretKey, bucketName string) (*Client, error) {
	// Menentukan apakah menggunakan SSL
	useSSL := true
	if strings.HasPrefix(endpoint, "http://") {
		useSSL = false
		endpoint = strings.TrimPrefix(endpoint, "http://")
	} else if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
	}

	// Supabase S3 Compatibility terkadang memerlukan parsing endpoint
	// Jika endpoint berisi path seperti /storage/v1/s3, minio client butuh endpoint dasar saja.
	// Tetapi biasanya, jika host-nya adalah <project-ref>.supabase.co, maka endpoint-nya cukup host tersebut.
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize minio client: %w", err)
	}

	client := &Client{
		minioClient: minioClient,
		bucketName:  bucketName,
	}

	GlobalClient = client
	return client, nil
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
