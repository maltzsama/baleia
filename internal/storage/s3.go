package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type S3Client struct {
	endpoint    string
	accessKey   string
	secretKey   string
	bucket      string
	useSSL      bool
	httpClient  *http.Client
}

func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) *S3Client {
	return &S3Client{
		endpoint:   strings.TrimRight(endpoint, "/"),
		accessKey:  accessKey,
		secretKey:  secretKey,
		bucket:     bucket,
		useSSL:     useSSL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *S3Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	u := fmt.Sprintf("%s://%s/%s/%s", scheme, c.endpoint, c.bucket, key)

	req, err := http.NewRequestWithContext(ctx, "PUT", u, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", strconv.Itoa(len(data)))

	c.signV2(req, key)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *S3Client) Get(ctx context.Context, key string) ([]byte, string, error) {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	u := fmt.Sprintf("%s://%s/%s/%s", scheme, c.endpoint, c.bucket, key)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}

	c.signV2(req, key)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("s3 get %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return data, resp.Header.Get("Content-Type"), nil
}

func (c *S3Client) GetReader(ctx context.Context, key string) (io.ReadCloser, string, error) {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	u := fmt.Sprintf("%s://%s/%s/%s", scheme, c.endpoint, c.bucket, key)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}

	c.signV2(req, key)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("s3 get %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

func (c *S3Client) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s/%s/%s", scheme, c.endpoint, c.bucket, key)

	expires := time.Now().Add(ttl).Unix()

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("AWSAccessKeyId", c.accessKey)
	q.Set("Expires", strconv.FormatInt(expires, 10))
	q.Set("Signature", c.presignSignature("GET", key, expires))
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *S3Client) signV2(req *http.Request, key string) {
	date := time.Now().UTC().Format(time.RFC1123Z)
	resource := "/" + c.bucket + "/" + key
	stringToSign := req.Method + "\n" +
		req.Header.Get("Content-MD5") + "\n" +
		req.Header.Get("Content-Type") + "\n" +
		date + "\n" +
		resource

	mac := hmac.New(sha1.New, []byte(c.secretKey))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req.Header.Set("Authorization", "AWS "+c.accessKey+":"+signature)
	req.Header.Set("Date", date)
}

func (c *S3Client) presignSignature(method, key string, expires int64) string {
	resource := "/" + c.bucket + "/" + key
	stringToSign := method + "\n\n\n" + strconv.FormatInt(expires, 10) + "\n" + resource

	mac := hmac.New(sha1.New, []byte(c.secretKey))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
