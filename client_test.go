package smswire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type seen struct {
	method, path, query string
	header              http.Header
	body                map[string]any
}

// recorder answers every request with handle and keeps what it saw.
func recorder(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) (*Client, *[]seen) {
	t.Helper()
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		reqs = append(reqs, seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Clone(), body})
		n := len(reqs)
		mu.Unlock()
		handle(w, r, n)
	}))
	t.Cleanup(srv.Close)
	client, err := New(WithAPIKey("smswire_live_test"), WithProject("shop"), WithBaseURL(srv.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	return client, &reqs
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

const msgJSON = `{"id":"msg_1","to":"+14155550123","status":"queued","error":null,"segments":1,"type":"transactional","tags":["orders"],"metadata":{"order":7},"batch_id":null,"received_at":"2026-11-10T09:00:00.123Z","scheduled_at":null,"sent_at":null,"delivered_at":null}`

func TestNewNeedsKeyAndProject(t *testing.T) {
	t.Setenv("SMSWIRE_KEY", "")
	t.Setenv("SMSWIRE_PROJECT", "")
	if _, err := New(WithProject("p")); err == nil || !strings.Contains(err.Error(), "SMSWIRE_KEY") {
		t.Fatalf("want a missing-key error, got %v", err)
	}
	if _, err := New(WithAPIKey("k")); err == nil || !strings.Contains(err.Error(), "SMSWIRE_PROJECT") {
		t.Fatalf("want a missing-project error, got %v", err)
	}
	t.Setenv("SMSWIRE_KEY", "k")
	t.Setenv("SMSWIRE_PROJECT", "p")
	t.Setenv("SMSWIRE_URL", "")
	c, err := New()
	if err != nil || c.baseURL != DefaultBaseURL || c.maxRetries != 2 {
		t.Fatalf("env defaults: %+v %v", c, err)
	}
}

func TestSendSendsJSONWithAnIdempotencyKey(t *testing.T) {
	client, reqs := recorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) { reply(w, 202, msgJSON) })
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	msg, err := client.Messages.Send(context.Background(), &SendMessageParams{To: "+14155550123", Body: "Hi", Tags: []string{"orders"}, Metadata: map[string]any{"order": 7}, SendAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "msg_1" || msg.Metadata["order"] != float64(7) || !msg.ReceivedAt.Equal(time.Date(2026, 11, 10, 9, 0, 0, 123e6, time.UTC)) {
		t.Fatalf("decoded %+v", msg)
	}
	r := (*reqs)[0]
	if r.method != "POST" || r.path != "/v1/p/shop/messages" {
		t.Fatalf("%s %s", r.method, r.path)
	}
	if r.header.Get("Authorization") != "Bearer smswire_live_test" || r.header.Get("Content-Type") != "application/json" || r.header.Get("User-Agent") != "smswire-go/"+Version {
		t.Fatalf("headers %v", r.header)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(r.header.Get("Idempotency-Key")) {
		t.Fatalf("idempotency key %q", r.header.Get("Idempotency-Key"))
	}
	want := map[string]any{"to": "+14155550123", "body": "Hi", "tags": []any{"orders"}, "metadata": map[string]any{"order": float64(7)}, "send_at": "2026-11-20T09:00:00Z"}
	if b, _ := json.Marshal(r.body); string(b) != mustJSON(want) {
		t.Fatalf("body %s", b)
	}

	_, _ = client.Messages.Send(context.Background(), &SendMessageParams{To: "+1", Body: "x"}, WithIdempotencyKey("order-42"))
	if got := (*reqs)[1].header.Get("Idempotency-Key"); got != "order-42" {
		t.Fatalf("own key: %q", got)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestASendIsRetriedWithTheSameKey(t *testing.T) {
	client, reqs := recorder(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			reply(w, 503, `{"error":"unavailable","message":"try again"}`)
			return
		}
		reply(w, 202, msgJSON)
	})
	if _, err := client.Messages.Send(context.Background(), &SendMessageParams{To: "+1", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 2 || (*reqs)[0].header.Get("Idempotency-Key") != (*reqs)[1].header.Get("Idempotency-Key") {
		t.Fatalf("want one retry with the same key, got %d requests", len(*reqs))
	}
}

func TestListEncodesFiltersAndAllPages(t *testing.T) {
	client, reqs := recorder(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Query().Get("cursor") == "" {
			reply(w, 200, `{"data":[`+msgJSON+`,`+strings.Replace(msgJSON, "msg_1", "msg_2", 1)+`],"next_cursor":"c2"}`)
			return
		}
		reply(w, 200, `{"data":[`+strings.Replace(msgJSON, "msg_1", "msg_3", 1)+`],"next_cursor":null}`)
	})
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	page, err := client.Messages.List(context.Background(), &ListMessagesParams{Status: StatusDelivered, Tag: "orders", From: from, Limit: 2})
	if err != nil || len(page.Data) != 2 || *page.NextCursor != "c2" {
		t.Fatalf("page %+v %v", page, err)
	}
	if q := (*reqs)[0].query; q != "from=2026-11-01T00%3A00%3A00Z&limit=2&status=delivered&tag=orders" {
		t.Fatalf("query %s", q)
	}

	var ids []string
	for m, err := range client.Messages.All(context.Background(), &ListMessagesParams{Tag: "orders", Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "msg_1,msg_2,msg_3" {
		t.Fatalf("all: %v", ids)
	}
	if q := (*reqs)[len(*reqs)-1].query; q != "cursor=c2&tag=orders" {
		t.Fatalf("second page query %s", q)
	}

	before := len(*reqs)
	for range client.Messages.All(context.Background(), nil) {
		break
	}
	if len(*reqs) != before+1 {
		t.Fatal("breaking out of All fetched more pages")
	}
}

func TestOTPVerifyAndScheduleCalls(t *testing.T) {
	client, reqs := recorder(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case r.Method == "DELETE":
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/verify"):
			reply(w, 200, `{"valid":false,"reason":"expired"}`)
		default:
			reply(w, 200, `{"id":"sch 1","name":"Weekly","cron":"0 9 * * 1","timezone":"UTC","enabled":false,"tags":[],"recipients":{"numbers":1,"group_ids":[]},"run_count":0,"created_at":"2026-11-10T09:00:00Z"}`)
		}
	})
	ctx := context.Background()
	v, err := client.OTP.Verify(ctx, "otp_1", "123456")
	if err != nil || v.Valid || *v.Reason != OTPExpired {
		t.Fatalf("verify %+v %v", v, err)
	}
	if b := (*reqs)[0].body; b["id"] != "otp_1" || b["code"] != "123456" {
		t.Fatalf("verify body %v", b)
	}
	s, err := client.Schedules.Update(ctx, "sch 1", &UpdateScheduleParams{Enabled: Ptr(false)})
	if err != nil || s.Enabled || s.Recipients.Numbers != 1 {
		t.Fatalf("update %+v %v", s, err)
	}
	if r := (*reqs)[1]; r.method != "PATCH" || r.path != "/v1/p/shop/schedules/sch%201" || mustJSON(r.body) != `{"enabled":false}` {
		t.Fatalf("update request %s %s %v", r.method, r.path, r.body)
	}
	if err := client.Schedules.Delete(ctx, "sch 1"); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkErrorsAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	client, _ := New(WithAPIKey("k"), WithProject("p"), WithBaseURL(url), WithMaxRetries(1))
	attempts := 0
	client.sleep = func(context.Context, time.Duration) error { attempts++; return nil }
	_, err := client.Messages.Get(context.Background(), "msg_1")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 0 || apiErr.Code != "network_error" || apiErr.Err == nil || attempts != 1 {
		t.Fatalf("want a retried network_error, got %v after %d waits", err, attempts)
	}

	slow, _ := recorder(t, func(w http.ResponseWriter, r *http.Request, _ int) { <-r.Context().Done() })
	slow.timeout = 50 * time.Millisecond
	slow.maxRetries = 0
	_, err = slow.Messages.Get(context.Background(), "msg_1")
	if !errors.As(err, &apiErr) || apiErr.Code != "timeout" {
		t.Fatalf("want timeout, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = slow.Messages.Get(ctx, "msg_1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWebhooksNeedASecret(t *testing.T) {
	t.Setenv("SMSWIRE_WEBHOOK_SECRET", "")
	client, _ := New(WithAPIKey("k"), WithProject("p"))
	if _, err := client.Webhooks.Verify([]byte(`{}`), "t=1,v1=00"); err == nil || !strings.Contains(err.Error(), "SMSWIRE_WEBHOOK_SECRET") {
		t.Fatalf("want a missing-secret error, got %v", err)
	}
}
