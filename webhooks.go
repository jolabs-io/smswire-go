package smswire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the header a webhook's signature arrives in.
const SignatureHeader = "X-SMSWire-Signature"

// Webhook event names (WebhookEvent.Type).
const (
	EventMessageStatus   = "message.status"
	EventMessageReceived = "message.received"
	EventAlertRaised     = "alert.raised"
	EventAlertResolved   = "alert.resolved"
	EventAlertTest       = "alert.test"
)

// WebhookEvent is a verified webhook. Exactly one of Status, Received and Alert is set, by Type.
type WebhookEvent struct {
	// "message.status", "message.received", "alert.raised", "alert.resolved" or "alert.test".
	Type     string
	Status   *StatusEvent
	Received *ReceivedEvent
	Alert    *AlertEvent
	// The body as it arrived.
	Raw json.RawMessage
}

// StatusEvent is a message.status webhook: a message you sent changed status.
type StatusEvent struct {
	Event       string         `json:"event"`
	ID          string         `json:"id"`
	ProjectID   *string        `json:"project_id"`
	To          string         `json:"to"`
	Status      MessageStatus  `json:"status"`
	Error       *string        `json:"error"`
	Segments    int            `json:"segments"`
	Type        MessageType    `json:"type"`
	Tags        []string       `json:"tags"`
	Metadata    map[string]any `json:"metadata"`
	BatchID     *string        `json:"batch_id"`
	SentAt      *time.Time     `json:"sent_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
}

// ReceivedEvent is a message.received webhook: the phone got an SMS.
type ReceivedEvent struct {
	Event      string    `json:"event"`
	ID         string    `json:"id"`
	ProjectID  *string   `json:"project_id"`
	From       string    `json:"from"`
	Body       string    `json:"body"`
	Sim        *int      `json:"sim"`
	ReceivedAt time.Time `json:"received_at"`
}

// AlertEvent is an alert.* webhook about a phone (offline, battery low…).
type AlertEvent struct {
	Event string `json:"event"`
	Alert Alert  `json:"alert"`
}

// Alert is what an AlertEvent reports.
type Alert struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Device struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"device"`
	Message    string     `json:"message"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

// WebhooksService verifies webhooks with the client's signing secret: Client.Webhooks.
type WebhooksService struct{ secret string }

// Verify checks a webhook against the secret from WithWebhookSecret or SMSWIRE_WEBHOOK_SECRET.
// See VerifyWebhook.
func (s *WebhooksService) Verify(rawBody []byte, signature string, opts ...VerifyOption) (*WebhookEvent, error) {
	if s.secret == "" {
		return nil, errors.New("smswire: pass WithWebhookSecret or set SMSWIRE_WEBHOOK_SECRET")
	}
	return VerifyWebhook(rawBody, signature, s.secret, opts...)
}

type verifyConfig struct {
	tolerance time.Duration
	now       func() time.Time
}

// VerifyOption configures VerifyWebhook.
type VerifyOption func(*verifyConfig)

// WithTolerance sets the oldest (and furthest in the future) signature accepted. Default 5 minutes.
func WithTolerance(d time.Duration) VerifyOption { return func(c *verifyConfig) { c.tolerance = d } }

// WithNow sets the current time; for tests.
func WithNow(now time.Time) VerifyOption {
	return func(c *verifyConfig) { c.now = func() time.Time { return now } }
}

var hex64 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseSignature reads `t=<unix>,v1=<hex>[,v1=<hex>…]`, parts in any order.
func parseSignature(header string) (int64, []string, bool) {
	t, haveT := int64(0), false
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case key == "t":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= 0 && !strings.HasPrefix(value, "+") {
				t, haveT = n, true
			}
		case key == "v1" && hex64.MatchString(value):
			sigs = append(sigs, strings.ToLower(value))
		}
	}
	return t, sigs, haveT && len(sigs) > 0
}

// VerifyWebhook checks a webhook's X-SMSWire-Signature against the raw request body (exactly as
// received, not re-encoded JSON) and returns the parsed event. Its errors match
// ErrInvalidSignature when the signature is missing, wrong or more than the tolerance off;
// answer those with 400.
//
//	body, _ := io.ReadAll(r.Body)
//	event, err := smswire.VerifyWebhook(body, r.Header.Get(smswire.SignatureHeader), secret)
func VerifyWebhook(rawBody []byte, signature, secret string, opts ...VerifyOption) (*WebhookEvent, error) {
	cfg := verifyConfig{tolerance: 5 * time.Minute, now: time.Now}
	for _, opt := range opts {
		opt(&cfg)
	}
	if secret == "" {
		return nil, &SignatureError{Reason: "no webhook signing secret given"}
	}
	t, sigs, ok := parseSignature(signature)
	if !ok {
		return nil, &SignatureError{Reason: "missing or malformed " + SignatureHeader + " header"}
	}
	age := cfg.now().Unix() - t
	if age < 0 {
		age = -age
	}
	if time.Duration(age)*time.Second > cfg.tolerance {
		return nil, &SignatureError{Reason: "signature timestamp is outside the tolerance"}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t, 10) + "."))
	mac.Write(rawBody)
	expected := []byte(hex.EncodeToString(mac.Sum(nil)))
	match := false
	for _, s := range sigs {
		if hmac.Equal([]byte(s), expected) {
			match = true
		}
	}
	if !match {
		return nil, &SignatureError{Reason: "signature does not match"}
	}
	return parseEvent(rawBody)
}

func parseEvent(raw []byte) (*WebhookEvent, error) {
	var head struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	ev := &WebhookEvent{Type: head.Event, Raw: append(json.RawMessage(nil), raw...)}
	var err error
	switch {
	case head.Event == EventMessageStatus:
		ev.Status = new(StatusEvent)
		err = json.Unmarshal(raw, ev.Status)
	case head.Event == EventMessageReceived:
		ev.Received = new(ReceivedEvent)
		err = json.Unmarshal(raw, ev.Received)
	case strings.HasPrefix(head.Event, "alert."):
		ev.Alert = new(AlertEvent)
		err = json.Unmarshal(raw, ev.Alert)
	}
	if err != nil {
		return nil, err
	}
	return ev, nil
}
