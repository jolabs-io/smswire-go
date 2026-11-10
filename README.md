# SMSWire for Go

Send SMS and one-time codes from your app through your own Android phones with [SMSWire](https://smswire.app).

```sh
go get github.com/jolabs-io/smswire-go
```

Go 1.23+. Standard library only. [Reference on pkg.go.dev](https://pkg.go.dev/github.com/jolabs-io/smswire-go).

## Set up

Each project on the dashboard has an API key and a slug (the part after `/p/` in its endpoint). Keep them in the environment:

```sh
SMSWIRE_KEY="smswire_live_…"
SMSWIRE_PROJECT="prj_…"
SMSWIRE_WEBHOOK_SECRET="whsec_…"   # only if you receive webhooks
# SMSWIRE_URL="https://api.smswire.app"   # your server, if you host SMSWire yourself
```

```go
import smswire "github.com/jolabs-io/smswire-go"

sms, err := smswire.New() // or smswire.New(smswire.WithAPIKey(…), smswire.WithProject(…), smswire.WithBaseURL(…))
if err != nil {
	log.Fatal(err)
}
```

A `Client` is safe for concurrent use; make one and share it.

## Send

```go
msg, err := sms.Messages.Send(ctx, &smswire.SendMessageParams{
	To:        "+251911234567",
	Body:      "Your order {{order}} shipped",
	Variables: map[string]any{"order": "A-1042"},
	Type:      smswire.TypeTransactional, // TypeOTP | TypeTransactional | TypeNotification | TypeMarketing
	Tags:      []string{"orders"},
	Metadata:  map[string]any{"user_id": 42}, // echoed back in webhooks
})
// msg.ID "msg_…", msg.Status "queued", msg.Segments 1, …

// Your own idempotency key makes your retries safe too (the SDK adds a random one otherwise).
sms.Messages.Send(ctx, params, smswire.WithIdempotencyKey("order-"+order.ID))

// Later: up to 30 days ahead.
sms.Messages.Send(ctx, &smswire.SendMessageParams{To: to, Body: body, SendAt: smswire.Ptr(time.Now().Add(time.Hour))})

sms.Messages.Get(ctx, msg.ID)
sms.Messages.Cancel(ctx, msg.ID) // before it reaches a phone

page, err := sms.Messages.List(ctx, &smswire.ListMessagesParams{Status: smswire.StatusFailed, Limit: 50})
for m, err := range sms.Messages.All(ctx, &smswire.ListMessagesParams{Tag: "orders"}) {
	if err != nil {
		return err
	}
	fmt.Println(m.ID, m.Status)
}

batch, err := sms.Messages.SendBulk(ctx, &smswire.SendBulkParams{
	Name: "Weekend sale",
	Body: "Hi {{name}}, 20% off this weekend",
	Rate: 30, // per minute
	Messages: []smswire.BulkItem{
		{To: "+251911234567", Variables: map[string]any{"name": "Abebe"}},
		{To: "+251922345678", Variables: map[string]any{"name": "Sara"}},
	},
})
// batch.BatchID, batch.Accepted 2, batch.Rejected [{Index, To, Reason}]
```

## One-time codes

```go
otp, err := sms.OTP.Send(ctx, &smswire.SendOTPParams{To: "+251911234567", Length: 6, TTL: 300})
// later, with what the user typed:
result, err := sms.OTP.Verify(ctx, otp.ID, code)
if !result.Valid {
	fmt.Println(*result.Reason) // invalid | expired | too_many_attempts | already_used
}
```

## Recurring sends

```go
schedule, err := sms.Schedules.Create(ctx, &smswire.CreateScheduleParams{
	Cron:     "0 9 * * 1",
	Name:     "Weekly report",
	Timezone: "Africa/Addis_Ababa",
	To:       "+251911234567",
	Body:     "Your weekly report is ready",
})
sms.Schedules.Update(ctx, schedule.ID, &smswire.UpdateScheduleParams{Enabled: smswire.Ptr(false)})
sms.Schedules.List(ctx)
sms.Schedules.Delete(ctx, schedule.ID)
```

## Webhooks

Status updates (`message.status`) and replies (`message.received`) are POSTed to the project's webhook URL, signed with `X-SMSWire-Signature`. Verify the **raw** body:

```go
http.HandleFunc("POST /hooks/smswire", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	event, err := sms.Webhooks.Verify(body, r.Header.Get(smswire.SignatureHeader))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest) // errors.Is(err, smswire.ErrInvalidSignature)
		return
	}
	switch event.Type {
	case smswire.EventMessageStatus:
		markOrder(event.Status.Metadata, event.Status.Status)
	case smswire.EventMessageReceived:
		handleReply(event.Received.From, event.Received.Body)
	}
	w.WriteHeader(http.StatusOK)
})
```

Without a client: `smswire.VerifyWebhook(body, signature, secret)`. The same event can arrive twice; make the handler idempotent.

## Errors and retries

Failed calls return an `*smswire.Error` with `Status` (0 when no answer came), `Code` (`invalid_request`, `invalid_api_key`, `rate_limited`, `daily_limit_reached`, `network_error`, `timeout`, …), `Message` and `RetryAfter`:

```go
var apiErr *smswire.Error
if errors.As(err, &apiErr) && apiErr.Code == "rate_limited" {
	// apiErr.RetryAfter says how long to wait
}
```

The SDK retries (default `WithMaxRetries(2)`, waiting `Retry-After` or backing off):

- reads, updates, deletes and `Messages.Send` (it carries an idempotency key): on 429, 5xx and network errors;
- `SendBulk`, `OTP.Send`, `OTP.Verify`, `Schedules.Create`: on 429 only, since a 5xx or a dropped connection may already have done the work.

A `Retry-After` longer than `WithMaxRetryWait` (60 s, e.g. a daily limit) returns at once. Each attempt has its own timeout (`WithTimeout`, 30 s); cancelling the context stops everything, waits included.

## Contributing

This repository is a read-only copy of `sdks/go` in the SMSWire monorepo, published on every release. Its tests use fixtures from that monorepo and skip here. Report problems to support@smswire.app.

MIT licensed.
