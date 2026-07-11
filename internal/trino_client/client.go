package trino_client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	user    string
	http    *http.Client
}

func New(baseURL, user string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		http:    &http.Client{},
	}
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type StatementStats struct {
	State      string `json:"state"`
	Rows       int64  `json:"processedRows"`
	Bytes      int64  `json:"processedBytes"`
	ElapsedMs  int64  `json:"elapsedTimeMillis"`
}

type StatementResponse struct {
	ID      string           `json:"id"`
	InfoURI string           `json:"infoUri"`
	NextURI string           `json:"nextUri"`
	Stats   StatementStats   `json:"stats"`
	Columns []Column         `json:"columns"`
	Data    [][]interface{}  `json:"data"`
	Error   *StatementError  `json:"error"`
}

type StatementError struct {
	Message    string `json:"message"`
	ErrorCode  string `json:"errorCode"`
	ErrorName  string `json:"errorName"`
	ErrorType  string `json:"errorType"`
}

type QueryResult struct {
	QueryID  string
	Columns  []Column
	Data     [][]interface{}
	RowCount int64
}

func (c *Client) Execute(ctx context.Context, sql, catalog, schema string) (*QueryResult, error) {
	reqURL := c.baseURL + "/v1/statement"

	body := bytes.NewBufferString(sql)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Trino-User", c.user)
	if catalog != "" {
		req.Header.Set("X-Trino-Catalog", catalog)
	}
	if schema != "" {
		req.Header.Set("X-Trino-Schema", schema)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino execute: %w", err)
	}
	defer resp.Body.Close()

	var sr StatementResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("trino decode: %w", err)
	}

	if sr.Error != nil {
		return nil, fmt.Errorf("trino error: %s (%s)", sr.Error.Message, sr.Error.ErrorName)
	}

	result := &QueryResult{
		QueryID: sr.ID,
		Columns: sr.Columns,
	}

	if sr.Data != nil {
		result.Data = append(result.Data, sr.Data...)
	}

	for sr.NextURI != "" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		sr, err = c.next(ctx, sr.NextURI)
		if err != nil {
			return nil, err
		}
		if sr.Error != nil {
			return nil, fmt.Errorf("trino error: %s (%s)", sr.Error.Message, sr.Error.ErrorName)
		}
		if sr.Data != nil {
			result.Data = append(result.Data, sr.Data...)
		}
	}

	result.RowCount = int64(len(result.Data))
	return result, nil
}

func (c *Client) Cancel(ctx context.Context, nextURI string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", nextURI, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("trino cancel failed: " + string(b))
	}
	return nil
}

func (c *Client) next(ctx context.Context, uri string) (StatementResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if err != nil {
		return StatementResponse{}, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return StatementResponse{}, fmt.Errorf("trino next: %w", err)
	}
	defer resp.Body.Close()

	var sr StatementResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return StatementResponse{}, fmt.Errorf("trino next decode: %w", err)
	}
	return sr, nil
}
