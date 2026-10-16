package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Client is the storage adapter for query results. It talks to any
// S3-compatible object store (AWS S3, MinIO, Ceph RGW, RustFS) through the
// minio-go client. Multipart upload for streaming readers (PutStream) is
// handled by minio-go's PutObject with size -1, so a result stream is never
// buffered in memory.
type S3Client struct {
	endpoint  string
	accessKey string
	secretKey string
	bucket    string
	useSSL    bool
	client    *minio.Client
	metrics   *metrics.Storage // nil-safe
}

// New creates the adapter. The endpoint is a host[:port] (no scheme).
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) *S3Client {
	ep := strings.TrimRight(endpoint, "/")
	if i := strings.Index(ep, "://"); i >= 0 {
		ep = ep[i+3:]
	}

	var opts *minio.Options
	if accessKey != "" || secretKey != "" {
		opts = &minio.Options{
			Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure:    useSSL,
			Transport: obs.Transport(nil),
		}
	} else {
		opts = &minio.Options{Secure: useSSL, Transport: obs.Transport(nil)}
	}

	mc, err := minio.New(ep, opts)
	if err != nil {
		// minio.New returns an error only when the config is unusable; surface
		// it as a panic-free placeholder so wiring at startup can propagate it.
		panic(fmt.Sprintf("storage: invalid s3 endpoint %q: %v", ep, err))
	}

	return &S3Client{
		endpoint:  ep,
		accessKey: accessKey,
		secretKey: secretKey,
		bucket:    bucket,
		useSSL:    useSSL,
		client:    mc,
	}
}

// MakeBucket ensures the bucket exists (idempotent).
func (c *S3Client) MakeBucket(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("s3 bucket exists: %w", err)
	}
	if exists {
		return nil
	}
	if err := c.client.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("s3 make bucket: %w", err)
	}
	return nil
}

// SetMetrics records operations in m (#87).
func (c *S3Client) SetMetrics(m *metrics.Storage) { c.metrics = m }

// Put uploads already-buffered bytes.
func (c *S3Client) Put(ctx context.Context, key string, data []byte, contentType string) (err error) {
	defer func() {
		var n int64
		if err == nil {
			n = int64(len(data))
		}
		c.metrics.Op("put", err, n)
	}()
	_, err = c.client.PutObject(ctx, c.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	return nil
}

// PutStream uploads r using S3 multipart upload (minio-go splits at 8 MiB) so
// unbounded-stream results are not held in memory. It returns the number of
// bytes written.
func (c *S3Client) PutStream(ctx context.Context, key string, r io.Reader, contentType string) (n int64, err error) {
	defer func() { c.metrics.Op("put", err, n) }()
	// size -1 tells minio-go to use multipart and read until EOF.
	info, err := c.client.PutObject(ctx, c.bucket, key, r, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("s3 putstream: %w", err)
	}
	return info.Size, nil
}

// Get downloads the object into memory and also returns its content type.
func (c *S3Client) Get(ctx context.Context, key string) (data []byte, contentType string, err error) {
	defer func() { c.metrics.Op("get", err, int64(len(data))) }()
	r, contentType, err := c.getReader(ctx, key)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()

	data, err = io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get read: %w", err)
	}
	return data, contentType, nil
}

// GetReader streams the object body, returning an io.ReadCloser plus the
// stored content type. The caller owns the reader and must close it.
// GetReader's bytes are not counted: the caller streams them.
func (c *S3Client) GetReader(ctx context.Context, key string) (_ io.ReadCloser, _ string, err error) {
	defer func() { c.metrics.Op("get", err, 0) }()
	return c.getReader(ctx, key)
}

func (c *S3Client) getReader(ctx context.Context, key string) (io.ReadCloser, string, error) {
	obj, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}

	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, "", fmt.Errorf("s3 get stat: %w", err)
	}

	return obj, st.ContentType, nil
}

// PresignedGetURL returns a temporary presigned URL for an object. It is not
// used for query results (those are served through the API); retained for
// administrative and upload flows that need a shareable link.
func (c *S3Client) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.client.PresignedGetObject(ctx, c.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("s3 presign: %w", err)
	}
	return u.String(), nil
}
