package extend

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNewSpiderTHS(t *testing.T) {
	ls, err := GetTHSDayKline("sz000001", THS_HFQ)
	if err != nil {
		t.Error(err)
		return
	}
	for _, v := range ls {
		t.Log(v)
	}
}

func TestDecodeTHSDayKlineJSONPRejectsEmptyBody(t *testing.T) {
	_, err := decodeTHSDayKlineJSONP(nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestDecodeTHSDayKlineJSONPRejectsMalformedBody(t *testing.T) {
	_, err := decodeTHSDayKlineJSONP([]byte(`{"total":0}`))
	if err == nil {
		t.Fatal("expected error for malformed body")
	}
}

func TestDecodeTHSDayKlineJSONPReturnsPayload(t *testing.T) {
	payload, err := decodeTHSDayKlineJSONP([]byte(`callback({"total":0})`))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if strings.TrimSpace(string(payload)) != `{"total":0}` {
		t.Fatalf("payload = %q", payload)
	}
}

func TestGetTHSDayKlineReturnsErrorForHTTPFailure(t *testing.T) {
	withTHSHTTPResponse(t, http.StatusNotFound, "")

	_, err := GetTHSDayKline("sz000001", THS_QFQ)
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "status=404") {
		t.Fatalf("error = %q, want status=404", err.Error())
	}
}

func TestGetTHSDayKlineReturnsErrorForEmptyBody(t *testing.T) {
	withTHSHTTPResponse(t, http.StatusOK, "")

	_, err := GetTHSDayKline("sz000001", THS_QFQ)
	if err == nil {
		t.Fatal("expected error for empty response body")
	}
	if !strings.Contains(err.Error(), "响应格式错误") {
		t.Fatalf("error = %q, want format error", err.Error())
	}
}

func withTHSHTTPResponse(t *testing.T, status int, body string) {
	t.Helper()
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
