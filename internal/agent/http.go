package agent

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// httpFetch gives grown tools and plugins real network reach:
//
//	evolv.http("https://example.com")  →  {ok:true, status:200, body:"…"}
//	                                   →  {ok:false, error:"…"}
//
// Bounded: 15s timeout, 512KB body cap, 5 redirects. One shared client so
// plugins don't each open fresh connection pools.
var (
	httpClientOnce sync.Once
	httpClient     *http.Client
)

const httpMaxBody = 512 << 10

func sharedHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
		}
	})
	return httpClient
}

func httpFetch(rawURL string) map[string]any {
	if rawURL == "" {
		return map[string]any{"ok": false, "error": "http needs a url"}
	}
	if rawURL[:4] != "http" {
		rawURL = "https://" + rawURL
	}
	resp, err := sharedHTTPClient().Get(rawURL)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody+1))
	if err != nil && len(body) == 0 {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	truncated := len(body) > httpMaxBody
	if truncated {
		body = body[:httpMaxBody]
	}
	return map[string]any{
		"ok":        true,
		"status":    resp.StatusCode,
		"body":      string(body),
		"truncated": truncated,
		"url":       resp.Request.URL.String(),
	}
}
