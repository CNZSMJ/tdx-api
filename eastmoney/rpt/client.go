package rpt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultEndpoint = "https://datacenter-web.eastmoney.com/api/data/v1/get"

type ClientConfig struct {
	Endpoint   string
	HTTPClient *http.Client
	PageSize   int
	RetryCount int
	Timeout    time.Duration
}

type Client struct {
	endpoint   string
	httpClient *http.Client
	pageSize   int
	retryCount int
}

type Query struct {
	ReportName  string
	Columns     []string
	Filter      string
	SortColumns []string
	SortTypes   []string
	PageNumber  int
	PageSize    int
	Params      map[string]string
}

type Page struct {
	Rows       []map[string]any
	Count      int
	Pages      int
	PageNumber int
}

func NewClient(cfg ClientConfig) *Client {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 5000
	}
	if cfg.RetryCount < 0 {
		cfg.RetryCount = 0
	}
	if cfg.RetryCount == 0 {
		cfg.RetryCount = 3
	}
	return &Client{
		endpoint:   cfg.Endpoint,
		httpClient: cfg.HTTPClient,
		pageSize:   cfg.PageSize,
		retryCount: cfg.RetryCount,
	}
}

func (c *Client) Query(ctx context.Context, query Query) (Page, error) {
	if strings.TrimSpace(query.ReportName) == "" {
		return Page{}, fmt.Errorf("eastmoney rpt query requires report name")
	}
	if query.PageNumber <= 0 {
		query.PageNumber = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = c.pageSize
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Page{}, ctx.Err()
			case <-timer.C:
			}
		}
		page, err := c.queryOnce(ctx, query)
		if err == nil {
			return page, nil
		}
		lastErr = err
	}
	return Page{}, lastErr
}

func (c *Client) QueryAll(ctx context.Context, query Query) ([]map[string]any, error) {
	pageNumber := 1
	rows := make([]map[string]any, 0, query.PageSize)
	for {
		query.PageNumber = pageNumber
		page, err := c.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		rows = append(rows, page.Rows...)
		if page.Pages > 0 {
			if pageNumber >= page.Pages {
				return rows, nil
			}
		} else if len(page.Rows) < effectivePageSize(query.PageSize, c.pageSize) {
			return rows, nil
		}
		pageNumber++
	}
}

func (c *Client) queryOnce(ctx context.Context, query Query) (Page, error) {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return Page{}, err
	}
	values := endpoint.Query()
	values.Set("reportName", query.ReportName)
	values.Set("pageNumber", strconv.Itoa(query.PageNumber))
	values.Set("pageSize", strconv.Itoa(query.PageSize))
	if len(query.Columns) > 0 {
		values.Set("columns", strings.Join(query.Columns, ","))
	}
	if query.Filter != "" {
		values.Set("filter", query.Filter)
	}
	if len(query.SortColumns) > 0 {
		values.Set("sortColumns", strings.Join(query.SortColumns, ","))
	}
	if len(query.SortTypes) > 0 {
		values.Set("sortTypes", strings.Join(query.SortTypes, ","))
	}
	for key, value := range query.Params {
		values.Set(key, value)
	}
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", "tdx-api/market-billboard")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Page{}, fmt.Errorf("eastmoney rpt http status %d", resp.StatusCode)
	}

	var payload struct {
		Success bool `json:"success"`
		Result  struct {
			Pages int              `json:"pages"`
			Count int              `json:"count"`
			Data  []map[string]any `json:"data"`
		} `json:"result"`
		Message string `json:"message"`
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return Page{}, err
	}
	if !payload.Success && payload.Result.Data == nil {
		if payload.Message == "" {
			payload.Message = "eastmoney rpt query failed"
		}
		return Page{}, fmt.Errorf("%s", payload.Message)
	}
	return Page{
		Rows:       payload.Result.Data,
		Count:      payload.Result.Count,
		Pages:      payload.Result.Pages,
		PageNumber: query.PageNumber,
	}, nil
}

func effectivePageSize(queryPageSize, defaultPageSize int) int {
	if queryPageSize > 0 {
		return queryPageSize
	}
	return defaultPageSize
}
