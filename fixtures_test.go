package smswire

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// The shared vectors in sdks/fixtures, which every SDK passes the same way. Outside the monorepo
// (the public mirror) they aren't there, and these tests skip.
func readFixture(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "fixtures", name))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("sdks/fixtures is only in the SMSWire monorepo")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookSignatureFixtures(t *testing.T) {
	var f struct {
		Secret    string `json:"secret"`
		Tolerance int    `json:"tolerance"`
		Cases     []struct {
			Name   string `json:"name"`
			Body   string `json:"body"`
			Header string `json:"header"`
			Now    int64  `json:"now"`
			Valid  bool   `json:"valid"`
			Event  string `json:"event"`
		} `json:"cases"`
	}
	readFixture(t, "webhooks.json", &f)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ev, err := VerifyWebhook([]byte(c.Body), c.Header, f.Secret, WithTolerance(time.Duration(f.Tolerance)*time.Second), WithNow(time.Unix(c.Now, 0)))
			if !c.Valid {
				if !errors.Is(err, ErrInvalidSignature) {
					t.Fatalf("want ErrInvalidSignature, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := c.Event
			if want == "" {
				want = EventMessageStatus
			}
			if ev.Type != want {
				t.Fatalf("event %q, want %q", ev.Type, want)
			}
			if (ev.Status != nil) != (want == EventMessageStatus) || (ev.Received != nil) != (want == EventMessageReceived) || (ev.Alert != nil) != (len(want) > 6 && want[:6] == "alert.") {
				t.Fatalf("wrong typed payload for %s: %+v", want, ev)
			}
		})
	}
}

type errorCase struct {
	Name       string            `json:"name"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	RetryAfter *float64          `json:"retry_after"`
	Retry      bool              `json:"retry"`
	RetryPost  bool              `json:"retry_post"`
}

// answerOnce serves the case's answer first and a success after; it counts requests.
func answerOnce(c errorCase, ok string) (*httptest.Server, *int32) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			for k, v := range c.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(c.Status)
			_, _ = w.Write([]byte(c.Body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ok))
	}))
	return srv, &n
}

func TestErrorFixtures(t *testing.T) {
	var f struct {
		MaxRetryWait int         `json:"max_retry_wait"`
		Cases        []errorCase `json:"cases"`
	}
	readFixture(t, "errors.json", &f)
	check := func(t *testing.T, c errorCase, retried bool, call func(*Client) error) {
		srv, n := answerOnce(c, `{"id":"msg_1","to":"+1","status":"queued","segments":1,"type":"otp","tags":[],"received_at":"2026-11-10T00:00:00Z","expires_at":"2026-11-10T00:05:00Z","message_id":"msg_1"}`)
		defer srv.Close()
		client, err := New(WithAPIKey("k"), WithProject("p"), WithBaseURL(srv.URL), WithMaxRetries(1), WithMaxRetryWait(time.Duration(f.MaxRetryWait)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		var waited []time.Duration
		client.sleep = func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }
		err = call(client)
		if retried {
			if err != nil || atomic.LoadInt32(n) != 2 {
				t.Fatalf("want a retry then success, got err=%v after %d requests", err, *n)
			}
			if c.RetryAfter != nil && (len(waited) != 1 || waited[0] != time.Duration(*c.RetryAfter*float64(time.Second))) {
				t.Fatalf("want a wait of Retry-After %vs, waited %v", *c.RetryAfter, waited)
			}
			return
		}
		var apiErr *Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("want *Error, got %v", err)
		}
		if apiErr.Status != c.Status || apiErr.Code != c.Code || apiErr.Message != c.Message {
			t.Fatalf("got %d %q %q, want %d %q %q", apiErr.Status, apiErr.Code, apiErr.Message, c.Status, c.Code, c.Message)
		}
		switch {
		case c.RetryAfter == nil && apiErr.RetryAfter != nil:
			t.Fatalf("want no RetryAfter, got %v", *apiErr.RetryAfter)
		case c.RetryAfter != nil && (apiErr.RetryAfter == nil || *apiErr.RetryAfter != time.Duration(*c.RetryAfter*float64(time.Second))):
			t.Fatalf("want RetryAfter %vs, got %v", *c.RetryAfter, apiErr.RetryAfter)
		}
		if atomic.LoadInt32(n) != 1 {
			t.Fatalf("want no retry, got %d requests", *n)
		}
	}
	for _, c := range f.Cases {
		t.Run("safe/"+c.Name, func(t *testing.T) {
			check(t, c, c.Retry, func(cl *Client) error { _, err := cl.Messages.Get(context.Background(), "msg_1"); return err })
		})
		t.Run("post/"+c.Name, func(t *testing.T) {
			check(t, c, c.RetryPost, func(cl *Client) error {
				_, err := cl.OTP.Send(context.Background(), &SendOTPParams{To: "+1"})
				return err
			})
		})
	}
}
