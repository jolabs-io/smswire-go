package smswire

import "time"

// MessageType sets a message's priority on the phone and how plan limits and opt-outs treat it.
type MessageType string

const (
	TypeOTP           MessageType = "otp"
	TypeTransactional MessageType = "transactional"
	TypeNotification  MessageType = "notification"
	// Marketing messages are refused to numbers that opted out (replied STOP).
	TypeMarketing MessageType = "marketing"
)

// MessageStatus is where a message is on its way to the recipient.
type MessageStatus string

const (
	StatusQueued    MessageStatus = "queued"
	StatusSending   MessageStatus = "sending"
	StatusSent      MessageStatus = "sent"
	StatusDelivered MessageStatus = "delivered"
	StatusFailed    MessageStatus = "failed"
	StatusCancelled MessageStatus = "cancelled"
)

// SendMessageParams is one SMS to queue. Give Body, or Template with its Variables.
type SendMessageParams struct {
	// Recipient, in international format (+14155550123).
	To       string `json:"to"`
	Body     string `json:"body,omitempty"`
	Template string `json:"template,omitempty"`
	// Values for the template's (or body's) {{placeholders}}: strings, numbers or booleans.
	Variables map[string]any `json:"variables,omitempty"`
	// SIM slot (1, 2…) on a dual-SIM phone; 0 uses the project's.
	Sim  int         `json:"sim,omitempty"`
	Type MessageType `json:"type,omitempty"`
	// Up to 10 labels (a-z, 0-9, _ and -), for filtering.
	Tags []string `json:"tags,omitempty"`
	// Your own data, echoed in webhooks.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Held until then (up to 30 days ahead); nil sends now.
	SendAt *time.Time `json:"send_at,omitempty"`
}

// Message is a message as the API reports it.
type Message struct {
	ID          string         `json:"id"`
	To          string         `json:"to"`
	Status      MessageStatus  `json:"status"`
	Error       *string        `json:"error"`
	Segments    int            `json:"segments"`
	Type        MessageType    `json:"type"`
	Tags        []string       `json:"tags"`
	Metadata    map[string]any `json:"metadata"`
	BatchID     *string        `json:"batch_id"`
	ReceivedAt  time.Time      `json:"received_at"`
	ScheduledAt *time.Time     `json:"scheduled_at"`
	SentAt      *time.Time     `json:"sent_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
	// Text and number erased by the plan's history period.
	Redacted bool `json:"redacted"`
}

// MessagePage is one page of messages, newest first.
type MessagePage struct {
	Data []Message `json:"data"`
	// Pass back as ListMessagesParams.Cursor for the next page; nil on the last.
	NextCursor *string `json:"next_cursor"`
}

// ListMessagesParams filters Messages.List. Zero values are left out.
type ListMessagesParams struct {
	Status  MessageStatus
	Type    MessageType
	Tag     string
	To      string
	BatchID string
	// Received at or after.
	From time.Time
	// Received before.
	Until  time.Time
	Cursor string
	// 1 to 200; default 50.
	Limit int
}

// BulkItem is one recipient of a bulk send; its fields override the batch's.
type BulkItem struct {
	To        string         `json:"to"`
	Body      string         `json:"body,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
	Type      MessageType    `json:"type,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// SendBulkParams is a batch: up to 10,000 recipients sent at a steady rate.
type SendBulkParams struct {
	Name     string     `json:"name,omitempty"`
	Messages []BulkItem `json:"messages"`
	// Messages per minute; 0 uses the phone's safe rate.
	Rate     int            `json:"rate,omitempty"`
	Sim      int            `json:"sim,omitempty"`
	Body     string         `json:"body,omitempty"`
	Template string         `json:"template,omitempty"`
	SendAt   *time.Time     `json:"send_at,omitempty"`
	Type     MessageType    `json:"type,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// BulkResult says how many recipients were queued, and why the others weren't.
type BulkResult struct {
	BatchID  string         `json:"batch_id"`
	Accepted int            `json:"accepted"`
	Rejected []RejectedItem `json:"rejected"`
}

// RejectedItem is a recipient left out of a batch (an invalid number, an opt-out…).
type RejectedItem struct {
	// Position in SendBulkParams.Messages.
	Index  int    `json:"index"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// SendOTPParams asks for a one-time code to be texted.
type SendOTPParams struct {
	To string `json:"to"`
	// Digits, 4 to 8; default 6.
	Length int `json:"length,omitempty"`
	// Seconds the code is valid, 60 to 1800; default 300.
	TTL int `json:"ttl,omitempty"`
	// The text, with {{code}} where the code goes.
	Body     string `json:"body,omitempty"`
	Template string `json:"template,omitempty"`
	Sim      int    `json:"sim,omitempty"`
}

// OTP is a code on its way. Keep ID for OTP.Verify.
type OTP struct {
	ID        string    `json:"id"`
	To        string    `json:"to"`
	ExpiresAt time.Time `json:"expires_at"`
	MessageID string    `json:"message_id"`
}

// OTPFailure is why a code didn't verify.
type OTPFailure string

const (
	OTPInvalid         OTPFailure = "invalid"
	OTPExpired         OTPFailure = "expired"
	OTPTooManyAttempts OTPFailure = "too_many_attempts"
	OTPAlreadyUsed     OTPFailure = "already_used"
)

// OTPVerification is the outcome of OTP.Verify.
type OTPVerification struct {
	Valid bool `json:"valid"`
	// Why not, when Valid is false.
	Reason *OTPFailure `json:"reason"`
}

// ScheduleRecipient is one number of a recurring send, with its own variables.
type ScheduleRecipient struct {
	To        string         `json:"to"`
	Variables map[string]any `json:"variables,omitempty"`
}

// CreateScheduleParams is a message (or batch) sent on a cron schedule. Give one of To, Messages
// or GroupIDs, and Body or Template.
type CreateScheduleParams struct {
	Name string `json:"name,omitempty"`
	// Five fields: minute hour day-of-month month day-of-week ("0 9 * * 1" is Mondays at 9:00).
	Cron string `json:"cron"`
	// IANA name, e.g. "Africa/Addis_Ababa"; default UTC.
	Timezone  string              `json:"timezone,omitempty"`
	To        string              `json:"to,omitempty"`
	Messages  []ScheduleRecipient `json:"messages,omitempty"`
	GroupIDs  []string            `json:"group_ids,omitempty"`
	Body      string              `json:"body,omitempty"`
	Template  string              `json:"template,omitempty"`
	Variables map[string]any      `json:"variables,omitempty"`
	Type      MessageType         `json:"type,omitempty"`
	Tags      []string            `json:"tags,omitempty"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
	Sim       int                 `json:"sim,omitempty"`
	// Messages per minute for a batch, 1 to 60.
	Rate    int        `json:"rate,omitempty"`
	EndsAt  *time.Time `json:"ends_at,omitempty"`
	MaxRuns int        `json:"max_runs,omitempty"`
	// Default true.
	Enabled *bool `json:"enabled,omitempty"`
}

// UpdateScheduleParams changes a schedule; nil and empty fields stay as they are. Recipients can't change.
type UpdateScheduleParams struct {
	Name     string      `json:"name,omitempty"`
	Cron     string      `json:"cron,omitempty"`
	Timezone string      `json:"timezone,omitempty"`
	Body     string      `json:"body,omitempty"`
	Template string      `json:"template,omitempty"`
	Type     MessageType `json:"type,omitempty"`
	EndsAt   *time.Time  `json:"ends_at,omitempty"`
	MaxRuns  *int        `json:"max_runs,omitempty"`
	Enabled  *bool       `json:"enabled,omitempty"`
}

// Schedule is a recurring send.
type Schedule struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Cron       string             `json:"cron"`
	Timezone   string             `json:"timezone"`
	Enabled    bool               `json:"enabled"`
	Body       *string            `json:"body"`
	TemplateID *string            `json:"template_id"`
	Type       *MessageType       `json:"type"`
	Tags       []string           `json:"tags"`
	Recipients ScheduleRecipients `json:"recipients"`
	NextRunAt  *time.Time         `json:"next_run_at"`
	LastRunAt  *time.Time         `json:"last_run_at"`
	RunCount   int                `json:"run_count"`
	EndsAt     *time.Time         `json:"ends_at"`
	MaxRuns    *int               `json:"max_runs"`
	CreatedAt  time.Time          `json:"created_at"`
	LastRun    *ScheduleRun       `json:"last_run"`
	// Recent runs; only from Schedules.Get.
	Runs []ScheduleRun `json:"runs"`
}

// ScheduleRecipients says who a schedule sends to.
type ScheduleRecipients struct {
	// Numbers given directly.
	Numbers  int      `json:"numbers"`
	GroupIDs []string `json:"group_ids"`
}

// ScheduleRun is one time a schedule fired.
type ScheduleRun struct {
	At time.Time `json:"at"`
	// "sent", "failed" or "skipped".
	Status    string  `json:"status"`
	MessageID *string `json:"message_id"`
	BatchID   *string `json:"batch_id"`
	Accepted  int     `json:"accepted"`
	Rejected  int     `json:"rejected"`
	Error     *string `json:"error"`
}

// SchedulePage lists the project's schedules.
type SchedulePage struct {
	Data []Schedule `json:"data"`
}
