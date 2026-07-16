package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	s3Service = "s3"
	s3Region  = "us-east-1"
)

type S3Client struct {
	endpoint   string
	accessKey  string
	secretKey  string
	bucket     string
	useSSL     bool
	httpClient *http.Client
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

func (c *S3Client) MakeBucket(ctx context.Context) error {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	u := fmt.Sprintf("%s://%s/%s", scheme, c.endpoint, c.bucket)

	req, err := http.NewRequestWithContext(ctx, "PUT", u, nil)
	if err != nil {
		return fmt.Errorf("s3 make bucket: %w", err)
	}
	req.Header.Set("Content-Length", "0")

	c.signV4(req, nil)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3 make bucket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 make bucket %d: %s", resp.StatusCode, string(body))
	}
	return nil
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

	c.signV4(req, data)

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

	c.signV4(req, nil)

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

	c.signV4(req, nil)

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

	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	expires := strconv.FormatInt(int64(ttl.Seconds()), 10)

	resource := "/" + c.bucket + "/" + key

	canonicalURI := resource
	canonicalQueryString := "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=" + url.QueryEscape(c.accessKey+"/"+dateStamp+"/"+s3Region+"/"+s3Service+"/aws4_request") +
		"&X-Amz-Date=" + amzDate +
		"&X-Amz-Expires=" + expires +
		"&X-Amz-SignedHeaders=host"
	canonicalHeaders := "host:" + c.endpoint + "\n"
	signedHeaders := "host"
	payloadHash := sha256Hex(nil)
	canonicalRequest := "GET\n" + canonicalURI + "\n" + canonicalQueryString + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := dateStamp + "/" + s3Region + "/" + s3Service + "/aws4_request"
	stringToSign := algorithm + "\n" + amzDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	signingKey := deriveSigningKey(c.secretKey, dateStamp, s3Region, s3Service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", c.accessKey+"/"+dateStamp+"/"+s3Region+"/"+s3Service+"/aws4_request")
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", expires)
	q.Set("X-Amz-SignedHeaders", "host")
	q.Set("X-Amz-Signature", signature)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *S3Client) signV4(req *http.Request, body []byte) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	payloadHash := sha256Hex(body)

	host := req.URL.Host
	if host == "" {
		host = c.endpoint
	}

	var headerNames []string
	for k := range req.Header {
		headerNames = append(headerNames, strings.ToLower(k))
	}
	headerNames = append(headerNames, "host")
	headerNames = append(headerNames, "x-amz-content-sha256")
	headerNames = append(headerNames, "x-amz-date")
	sort.Strings(headerNames)

	var canonicalHeaders strings.Builder
	var signedHeaders strings.Builder
	for i, name := range headerNames {
		if i > 0 {
			signedHeaders.WriteString(";")
		}
		signedHeaders.WriteString(name)

		var value string
		switch name {
		case "host":
			value = host
		case "x-amz-content-sha256":
			value = payloadHash
		case "x-amz-date":
			value = amzDate
		default:
			value = req.Header.Get(name)
		}
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteString("\n")
	}

	canonicalURI := req.URL.Path
	canonicalQueryString := req.URL.RawQuery
	canonicalRequest := req.Method + "\n" + canonicalURI + "\n" + canonicalQueryString + "\n" +
		canonicalHeaders.String() + "\n" + signedHeaders.String() + "\n" + payloadHash

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := dateStamp + "/" + s3Region + "/" + s3Service + "/aws4_request"
	stringToSign := algorithm + "\n" + amzDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	signingKey := deriveSigningKey(c.secretKey, dateStamp, s3Region, s3Service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", algorithm+" Credential="+c.accessKey+"/"+credentialScope+", SignedHeaders="+signedHeaders.String()+", Signature="+signature)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kSecret := []byte("AWS4" + secretKey)
	kDate := hmacSHA256(kSecret, []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func sha256Hex(data []byte) string {
	if data == nil {
		data = []byte{}
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
