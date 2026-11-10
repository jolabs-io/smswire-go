package smswire

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MessagesService sends, reads and cancels messages: Client.Messages.
type MessagesService struct {
	client *Client
	base   string
}

type sendConfig struct{ idempotencyKey string }

// SendOption configures Messages.Send.
type SendOption func(*sendConfig)

// WithIdempotencyKey makes repeating a send with the same key return the first message instead of
// sending again. Without it the SDK uses a fresh random key, so its own retries never send twice;
// pass your own (an order id, say) to make your retries safe too.
func WithIdempotencyKey(key string) SendOption { return func(c *sendConfig) { c.idempotencyKey = key } }

// Send queues one SMS for the project's phone.
func (s *MessagesService) Send(ctx context.Context, params *SendMessageParams, opts ...SendOption) (*Message, error) {
	cfg := sendConfig{idempotencyKey: newIdempotencyKey()}
	for _, opt := range opts {
		opt(&cfg)
	}
	var out Message
	err := s.client.do(ctx, request{method: http.MethodPost, path: s.base, body: params, headers: map[string]string{"Idempotency-Key": cfg.idempotencyKey}, idempotent: true}, &out)
	return result(&out, err)
}

// SendBulk queues a batch. Invalid or opted-out recipients come back in Rejected; the rest go out as one batch.
func (s *MessagesService) SendBulk(ctx context.Context, params *SendBulkParams) (*BulkResult, error) {
	var out BulkResult
	err := s.client.do(ctx, request{method: http.MethodPost, path: s.base + "/bulk", body: params}, &out)
	return result(&out, err)
}

// Get returns one message.
func (s *MessagesService) Get(ctx context.Context, id string) (*Message, error) {
	var out Message
	err := s.client.do(ctx, request{method: http.MethodGet, path: s.base + "/" + url.PathEscape(id), idempotent: true}, &out)
	return result(&out, err)
}

// Cancel stops a message that hasn't reached a phone yet.
func (s *MessagesService) Cancel(ctx context.Context, id string) (*Message, error) {
	var out Message
	err := s.client.do(ctx, request{method: http.MethodDelete, path: s.base + "/" + url.PathEscape(id), idempotent: true}, &out)
	return result(&out, err)
}

// List returns one page, newest first. Pass NextCursor back as Cursor for the next one, or use All.
func (s *MessagesService) List(ctx context.Context, params *ListMessagesParams) (*MessagePage, error) {
	var out MessagePage
	err := s.client.do(ctx, request{method: http.MethodGet, path: s.base, query: params.values(), idempotent: true}, &out)
	return result(&out, err)
}

// All yields every matching message, newest first, fetching pages as it goes. Params.Cursor is ignored.
//
//	for msg, err := range client.Messages.All(ctx, &smswire.ListMessagesParams{Tag: "orders"}) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(msg.ID, msg.Status)
//	}
func (s *MessagesService) All(ctx context.Context, params *ListMessagesParams) iter.Seq2[*Message, error] {
	return func(yield func(*Message, error) bool) {
		p := ListMessagesParams{}
		if params != nil {
			p = *params
		}
		p.Cursor = ""
		for {
			page, err := s.List(ctx, &p)
			if err != nil {
				yield(nil, err)
				return
			}
			for i := range page.Data {
				if !yield(&page.Data[i], nil) {
					return
				}
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				return
			}
			p.Cursor = *page.NextCursor
		}
	}
}

func (p *ListMessagesParams) values() url.Values {
	v := url.Values{}
	if p == nil {
		return v
	}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("status", string(p.Status))
	set("type", string(p.Type))
	set("tag", p.Tag)
	set("to", p.To)
	set("batch_id", p.BatchID)
	if !p.From.IsZero() {
		v.Set("from", p.From.UTC().Format(time.RFC3339Nano))
	}
	if !p.Until.IsZero() {
		v.Set("until", p.Until.UTC().Format(time.RFC3339Nano))
	}
	set("cursor", p.Cursor)
	if p.Limit > 0 {
		v.Set("limit", strconv.Itoa(p.Limit))
	}
	return v
}

// OTPService sends and checks one-time codes: Client.OTP.
type OTPService struct {
	client *Client
	base   string
}

// Send texts a fresh code. Keep the returned ID for Verify.
func (s *OTPService) Send(ctx context.Context, params *SendOTPParams) (*OTP, error) {
	var out OTP
	err := s.client.do(ctx, request{method: http.MethodPost, path: s.base, body: params}, &out)
	return result(&out, err)
}

// Verify checks what the user typed. Valid is false, with a Reason, when it's wrong, expired or used.
func (s *OTPService) Verify(ctx context.Context, id, code string) (*OTPVerification, error) {
	var out OTPVerification
	body := map[string]string{"id": id, "code": code}
	err := s.client.do(ctx, request{method: http.MethodPost, path: s.base + "/verify", body: body}, &out)
	return result(&out, err)
}

// SchedulesService manages recurring sends: Client.Schedules.
type SchedulesService struct {
	client *Client
	base   string
}

// Create starts a message (or batch) sent again and again on a cron schedule.
func (s *SchedulesService) Create(ctx context.Context, params *CreateScheduleParams) (*Schedule, error) {
	var out Schedule
	err := s.client.do(ctx, request{method: http.MethodPost, path: s.base, body: params}, &out)
	return result(&out, err)
}

// List returns the project's schedules.
func (s *SchedulesService) List(ctx context.Context) (*SchedulePage, error) {
	var out SchedulePage
	err := s.client.do(ctx, request{method: http.MethodGet, path: s.base, idempotent: true}, &out)
	return result(&out, err)
}

// Get returns one schedule, with its recent runs.
func (s *SchedulesService) Get(ctx context.Context, id string) (*Schedule, error) {
	var out Schedule
	err := s.client.do(ctx, request{method: http.MethodGet, path: s.base + "/" + url.PathEscape(id), idempotent: true}, &out)
	return result(&out, err)
}

// Update changes a schedule; fields left empty stay as they are.
func (s *SchedulesService) Update(ctx context.Context, id string, params *UpdateScheduleParams) (*Schedule, error) {
	var out Schedule
	err := s.client.do(ctx, request{method: http.MethodPatch, path: s.base + "/" + url.PathEscape(id), body: params, idempotent: true}, &out)
	return result(&out, err)
}

// Delete removes a schedule.
func (s *SchedulesService) Delete(ctx context.Context, id string) error {
	return s.client.do(ctx, request{method: http.MethodDelete, path: s.base + "/" + url.PathEscape(id), idempotent: true}, nil)
}

func result[T any](out *T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	return out, nil
}
