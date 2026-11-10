package smswire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Against the real API (apps/api's sdk-server: in-memory database, a phone that delivers at once).
// Skipped outside the monorepo, or when the API's dependencies aren't installed.

type sdkServer struct {
	BaseURL       string `json:"baseUrl"`
	Project       string `json:"project"`
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret"`
	Control       string `json:"control"`
}

func startSDKServer(t *testing.T) sdkServer {
	t.Helper()
	api, _ := filepath.Abs(filepath.Join("..", "..", "apps", "api"))
	tsx := filepath.Join(api, "node_modules", ".bin", "tsx")
	if _, err := os.Stat(tsx); err != nil {
		t.Skip("needs apps/api with its dependencies (pnpm install)")
	}
	cmd := exec.Command(tsx, "test/sdkServer.ts")
	cmd.Dir = api
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("sdk-server did not start: %v", err)
	}
	var s sdkServer
	if err := json.Unmarshal(line, &s); err != nil {
		t.Fatalf("sdk-server said %q: %v", line, err)
	}
	return s
}

func control(t *testing.T, s sdkServer, path string, body any, out any) {
	t.Helper()
	method, payload := http.MethodGet, []byte(nil)
	if body != nil {
		method, payload = http.MethodPost, []byte(mustJSON(body))
	}
	req, _ := http.NewRequest(method, s.Control+path, bytes.NewReader(payload))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestIntegration(t *testing.T) {
	s := startSDKServer(t)
	ctx := context.Background()
	sms, err := New(WithAPIKey(s.APIKey), WithProject(s.Project), WithBaseURL(s.BaseURL), WithWebhookSecret(s.WebhookSecret))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("sends a message that the phone delivers", func(t *testing.T) {
		sent, err := sms.Messages.Send(ctx, &SendMessageParams{To: "+251911234567", Body: "Your order shipped", Tags: []string{"orders"}, Metadata: map[string]any{"order": 7}})
		if err != nil {
			t.Fatal(err)
		}
		if sent.Status != StatusQueued || sent.Tags[0] != "orders" || sent.Metadata["order"] != float64(7) {
			t.Fatalf("sent %+v", sent)
		}
		var got *Message
		eventually(t, func() bool {
			got, err = sms.Messages.Get(ctx, sent.ID)
			return err == nil && got.Status == StatusDelivered
		})
		if got.DeliveredAt == nil {
			t.Fatal("no delivered_at")
		}
		found := false
		for m, err := range sms.Messages.All(ctx, &ListMessagesParams{Tag: "orders"}) {
			if err != nil {
				t.Fatal(err)
			}
			found = found || m.ID == sent.ID
		}
		if !found {
			t.Fatal("not listed under its tag")
		}
	})

	t.Run("replays a send with the same idempotency key", func(t *testing.T) {
		a, err := sms.Messages.Send(ctx, &SendMessageParams{To: "+251911234567", Body: "Once"}, WithIdempotencyKey("go-order-42"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := sms.Messages.Send(ctx, &SendMessageParams{To: "+251911234567", Body: "Once"}, WithIdempotencyKey("go-order-42"))
		if err != nil || a.ID != b.ID {
			t.Fatalf("%v %v %v", a.ID, b, err)
		}
	})

	t.Run("cancels a scheduled message", func(t *testing.T) {
		m, err := sms.Messages.Send(ctx, &SendMessageParams{To: "+251911234567", Body: "Later", SendAt: Ptr(time.Now().Add(time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		c, err := sms.Messages.Cancel(ctx, m.ID)
		if err != nil || c.Status != StatusCancelled {
			t.Fatalf("%+v %v", c, err)
		}
	})

	t.Run("sends a batch and rejects bad numbers", func(t *testing.T) {
		r, err := sms.Messages.SendBulk(ctx, &SendBulkParams{
			Name: "SDK test",
			Body: "Hi {{name}}",
			Messages: []BulkItem{
				{To: "+251911234567", Variables: map[string]any{"name": "Abebe"}},
				{To: "not a number", Variables: map[string]any{"name": "Nobody"}},
			},
		})
		if err != nil || r.Accepted != 1 || len(r.Rejected) != 1 || r.Rejected[0].Index != 1 || r.Rejected[0].To != "not a number" {
			t.Fatalf("%+v %v", r, err)
		}
	})

	t.Run("sends and verifies a one-time code", func(t *testing.T) {
		otp, err := sms.OTP.Send(ctx, &SendOTPParams{To: "+251933456789", Length: 6})
		if err != nil {
			t.Fatal(err)
		}
		var body string
		eventually(t, func() bool {
			var sent []struct{ MessageID, Body string }
			control(t, s, "/sms", nil, &sent)
			for _, m := range sent {
				if m.MessageID == otp.MessageID {
					body = m.Body
				}
			}
			return body != ""
		})
		code := regexp.MustCompile(`\d{6}`).FindString(body)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		if v, err := sms.OTP.Verify(ctx, otp.ID, wrong); err != nil || v.Valid || *v.Reason != OTPInvalid {
			t.Fatalf("wrong code: %+v %v", v, err)
		}
		if v, err := sms.OTP.Verify(ctx, otp.ID, code); err != nil || !v.Valid {
			t.Fatalf("right code: %+v %v", v, err)
		}
		if v, err := sms.OTP.Verify(ctx, otp.ID, code); err != nil || *v.Reason != OTPAlreadyUsed {
			t.Fatalf("used code: %+v %v", v, err)
		}
	})

	t.Run("manages a recurring send", func(t *testing.T) {
		created, err := sms.Schedules.Create(ctx, &CreateScheduleParams{Cron: "0 9 * * 1", Name: "Weekly", Timezone: "Africa/Addis_Ababa", To: "+251911234567", Body: "Weekly report"})
		if err != nil || created.Name != "Weekly" || !created.Enabled || created.Recipients.Numbers != 1 {
			t.Fatalf("%+v %v", created, err)
		}
		if u, err := sms.Schedules.Update(ctx, created.ID, &UpdateScheduleParams{Enabled: Ptr(false)}); err != nil || u.Enabled {
			t.Fatalf("%+v %v", u, err)
		}
		page, err := sms.Schedules.List(ctx)
		if err != nil || len(page.Data) == 0 {
			t.Fatalf("%+v %v", page, err)
		}
		if g, err := sms.Schedules.Get(ctx, created.ID); err != nil || g.ID != created.ID {
			t.Fatalf("%+v %v", g, err)
		}
		if err := sms.Schedules.Delete(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		var apiErr *Error
		if _, err := sms.Schedules.Get(ctx, created.ID); !errors.As(err, &apiErr) || apiErr.Status != 404 {
			t.Fatalf("want 404, got %v", err)
		}
	})

	t.Run("verifies the webhooks the API really sends", func(t *testing.T) {
		control(t, s, "/reply", map[string]string{"from": "+251911234567", "body": "ሰላም, thanks!"}, nil)
		type hook struct {
			Body    string            `json:"body"`
			Headers map[string]string `json:"headers"`
		}
		var hooks []hook
		eventually(t, func() bool {
			control(t, s, "/webhooks", nil, &hooks)
			for _, h := range hooks {
				if h.Headers["X-SMSWire-Event"] == EventMessageReceived {
					return true
				}
			}
			return false
		})
		for _, h := range hooks {
			ev, err := sms.Webhooks.Verify([]byte(h.Body), h.Headers[SignatureHeader])
			if err != nil || ev.Type != h.Headers["X-SMSWire-Event"] {
				t.Fatalf("%v %v", ev, err)
			}
			if ev.Type == EventMessageReceived && ev.Received.Body != "ሰላም, thanks!" {
				t.Fatalf("received %+v", ev.Received)
			}
		}
		tampered := strings.Replace(hooks[0].Body, "+251", "+252", 1)
		if _, err := VerifyWebhook([]byte(tampered), hooks[0].Headers[SignatureHeader], s.WebhookSecret); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("tampered body: %v", err)
		}
	})

	t.Run("turns API errors into *Error", func(t *testing.T) {
		wrong, _ := New(WithAPIKey("smswire_live_wrong"), WithProject(s.Project), WithBaseURL(s.BaseURL))
		var apiErr *Error
		if _, err := wrong.Messages.List(ctx, nil); !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != "invalid_api_key" {
			t.Fatalf("wrong key: %v", err)
		}
		if _, err := sms.Messages.Send(ctx, &SendMessageParams{To: "+251911234567"}); !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "invalid_request" {
			t.Fatalf("no body: %v", err)
		}
	})
}
