package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

// New creates the adapter. The endpoint is a host[:port] (no scheme). It
// fails only on an unusable configuration.
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3Client, error) {
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
		return nil, fmt.Errorf("storage: invalid s3 endpoint %q: %w", ep, err)
	}

	return &S3Client{
		endpoint:  ep,
		accessKey: accessKey,
		secretKey: secretKey,
		bucket:    bucket,
		useSSL:    useSSL,
		client:    mc,
	}, nil
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

// Health checks that the object store is reachable and the bucket exists. The
// caller maps the error to a stable code; the error itself is never exposed.
func (c *S3Client) Health(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("s3 health: %w", err)
	}
	if !exists {
		return fmt.Errorf("s3 health: bucket %q is missing", c.bucket)
	}
	return nil
}

// SetMetrics records operations in m (#87).
func (c *S3Client) SetMetrics(m *metrics.Storage) { c.metrics = m }

// Put uploads already-buffered bytes.
func (c *S3Client) Put(ctx context.Context, key string, data []byte, contentType string) (err error) {
	ctx, span := tracer.Start(ctx, "s3.put")
	defer func() {
		var n int64
		if err == nil {
			n = int64(len(data))
		}
		c.metrics.Op("put", err, n)
		endSpan(span, n, err)
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
	ctx, span := tracer.Start(ctx, "s3.put")
	defer func() {
		c.metrics.Op("put", err, n)
		endSpan(span, n, err)
	}()
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
	ctx, span := tracer.Start(ctx, "s3.get")
	defer func() {
		c.metrics.Op("get", err, int64(len(data)))
		endSpan(span, int64(len(data)), err)
	}()
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
	// The span covers opening the object; the caller streams the body.
	ctx, span := tracer.Start(ctx, "s3.get")
	defer func() {
		c.metrics.Op("get", err, 0)
		endSpan(span, 0, err)
	}()
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

// ObjectReaderAt reads byte ranges of an S3 object on demand. It implements
// io.ReaderAt + io.Seeker so the Arrow file reader can seek through a result
// (footer, then the batches it needs) without downloading the whole object
// (#238).
type ObjectReaderAt struct {
	ctx    context.Context
	client *minio.Client
	bucket string
	key    string
	size   int64
	pos    int64
}

// NewReaderAt stats the object and returns a range reader over it.
func (c *S3Client) NewReaderAt(ctx context.Context, key string) (*ObjectReaderAt, error) {
	st, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 stat: %w", err)
	}
	return &ObjectReaderAt{ctx: ctx, client: c.client, bucket: c.bucket, key: key, size: st.Size}, nil
}

// Size is the object size in bytes.
func (r *ObjectReaderAt) Size() int64 { return r.size }

// ReadAt fetches p from the object at off with one ranged GET. A short read at
// the end of the object returns io.EOF.
func (r *ObjectReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size || len(p) == 0 {
		return 0, io.EOF
	}
	length := int64(len(p))
	if off+length > r.size {
		length = r.size - off
	}
	opts := minio.GetObjectOptions{}
	// SetRange's end is inclusive (bytes=start-end).
	if err := opts.SetRange(off, off+length-1); err != nil {
		return 0, fmt.Errorf("s3 set range: %w", err)
	}
	obj, err := r.client.GetObject(r.ctx, r.bucket, r.key, opts)
	if err != nil {
		return 0, fmt.Errorf("s3 get: %w", err)
	}
	defer obj.Close()
	n, err := io.ReadFull(obj, p[:length])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("s3 read range: %w", err)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Read reads from the current position.
func (r *ObjectReaderAt) Read(p []byte) (int, error) {
	n, err := r.ReadAt(p, r.pos)
	r.pos += int64(n)
	return n, err
}

// Seek moves the current position; io.SeekEnd is relative to the object size.
func (r *ObjectReaderAt) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = offset
	case io.SeekCurrent:
		r.pos += offset
	case io.SeekEnd:
		r.pos = r.size + offset
	default:
		return 0, fmt.Errorf("s3 seek: invalid whence %d", whence)
	}
	return r.pos, nil
}

// tracer names the result store's spans (#380). Object keys are not span
// attributes: they name a user's executions.
var tracer = obs.Tracer("storage")

// endSpan records the bytes moved and ends span, failed when err is set.
func endSpan(span trace.Span, n int64, err error) {
	span.SetAttributes(attribute.Int64("baleia.bytes", n))
	obs.FailSpan(span, err, "object store operation failed")
	span.End()
}
