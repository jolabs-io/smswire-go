package smswire

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type request struct {
	method  string
	path    string
	query   url.Values
	body    any
	headers map[string]string
	// Safe to repeat after a 5xx or a dropped connection: GET, PATCH, DELETE, or a POST with an Idempotency-Key.
	idempotent bool
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryAfter reads a Retry-After header: seconds, or an HTTP date.
func retryAfter(header string) *time.Duration {
	if header == "" {
		return nil
	}
	if s, err := strconv.ParseFloat(header, 64); err == nil {
		d := time.Duration(math.Max(0, s) * float64(time.Second))
		return &d
	}
	if at, err := http.ParseTime(header); err == nil {
		d := max(0, time.Until(at))
		return &d
	}
	return nil
}

// backoff is exponential from 0.5 s, capped at 8 s, with up to 25% jitter.
func backoff(attempt int) time.Duration {
	base := math.Min(8, 0.5*math.Pow(2, float64(attempt)))
	return time.Duration(base * (1 + mrand.Float64()*0.25) * float64(time.Second))
}

// retryDelay says whether to try err again after attempt (0-based), and after how long.
func (c *Client) retryDelay(err *Error, idempotent bool, attempt int) (time.Duration, bool) {
	if attempt >= c.maxRetries {
		return 0, false
	}
	retryable := err.Status == http.StatusTooManyRequests || (idempotent && (err.Status == 0 || err.Status >= 500))
	if !retryable {
		return 0, false
	}
	if err.RetryAfter != nil {
		return *err.RetryAfter, *err.RetryAfter <= c.maxRetryWait
	}
	return backoff(attempt), true
}

// errorFrom turns a non-2xx answer into an *Error.
func errorFrom(res *http.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	e := &Error{Status: res.StatusCode, Code: "http_error", Message: fmt.Sprintf("HTTP %d", res.StatusCode), RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	var body struct {
		Error   *string `json:"error"`
		Message *string `json:"message"`
	}
	// Not {error, message} JSON (a proxy's error page): keep the generic code.
	if json.Unmarshal(raw, &body) == nil && body.Error != nil && body.Message != nil {
		e.Code, e.Message = *body.Error, *body.Message
	}
	return e
}

func (c *Client) do(ctx context.Context, req request, out any) error {
	u := c.baseURL + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}
	var payload []byte
	if req.body != nil {
		var err error
		if payload, err = json.Marshal(req.body); err != nil {
			return fmt.Errorf("smswire: encoding the request: %w", err)
		}
	}
	for attempt := 0; ; attempt++ {
		apiErr, err := c.attempt(ctx, req, u, payload, out)
		if apiErr == nil {
			return err
		}
		wait, again := c.retryDelay(apiErr, req.idempotent, attempt)
		if !again {
			return apiErr
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// attempt makes one request: an *Error to maybe retry, or the final result (nil, or an error that isn't the API's).
func (c *Client) attempt(ctx context.Context, req request, u string, payload []byte, out any) (*Error, error) {
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	hreq, err := http.NewRequestWithContext(actx, req.method, u, body)
	if err != nil {
		return nil, fmt.Errorf("smswire: building the request: %w", err)
	}
	hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.headers {
		hreq.Header.Set(k, v)
	}
	res, err := c.http.Do(hreq)
	if err != nil {
		// The caller gave up: that isn't ours to retry.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return &Error{Code: "timeout", Message: fmt.Sprintf("no answer within %s", c.timeout), Err: err}, nil
		}
		return &Error{Code: "network_error", Message: "could not reach " + c.baseURL + ": " + err.Error(), Err: err}, nil
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return errorFrom(res), nil
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return &Error{Code: "network_error", Message: "reading the answer: " + err.Error(), Err: err}, nil
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("smswire: decoding the answer: %w", err)
	}
	return nil, nil
}

// newIdempotencyKey is a random UUID (v4), so the SDK's own retries of a send never send twice.
func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
