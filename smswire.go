// Package smswire is the official Go SDK for SMSWire: send SMS and one-time codes from your app
// through your own Android phones, manage recurring sends, and verify webhooks.
//
//	client, err := smswire.New() // SMSWIRE_KEY, SMSWIRE_PROJECT, SMSWIRE_URL
//	if err != nil {
//		log.Fatal(err)
//	}
//	msg, err := client.Messages.Send(ctx, &smswire.SendMessageParams{To: "+14155550123", Body: "Hello from SMSWire"})
//
// Every call takes a context. Failed calls return an *Error with the HTTP status, the API's
// error code and, when the API says, how long to wait. Safe calls are retried after a 429, a 5xx
// or a dropped connection; see the README for which ones.
package smswire

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version is this SDK's version, sent in the User-Agent header.
const Version = "0.1.0"

// DefaultBaseURL is the API used when neither WithBaseURL nor SMSWIRE_URL says otherwise.
const DefaultBaseURL = "https://api.smswire.app"

// Client calls one project's API. It is safe for concurrent use.
type Client struct {
	Messages  *MessagesService
	OTP       *OTPService
	Schedules *SchedulesService
	Webhooks  *WebhooksService

	apiKey       string
	baseURL      string
	maxRetries   int
	maxRetryWait time.Duration
	timeout      time.Duration
	http         *http.Client
	userAgent    string
	// Waits between retries; tests swap it for one that doesn't sleep.
	sleep func(ctx context.Context, d time.Duration) error
}

type config struct {
	apiKey, project, baseURL, webhookSecret string
	maxRetries                              int
	maxRetryWait, timeout                   time.Duration
	http                                    *http.Client
}

// Option configures a Client.
type Option func(*config)

// WithAPIKey sets the project's API key (smswire_live_…). Defaults to SMSWIRE_KEY.
func WithAPIKey(key string) Option { return func(c *config) { c.apiKey = key } }

// WithProject sets the project's slug, the part after /p/ in its endpoint. Defaults to SMSWIRE_PROJECT.
func WithProject(slug string) Option { return func(c *config) { c.project = slug } }

// WithBaseURL sets the API's address. Defaults to SMSWIRE_URL, then https://api.smswire.app.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithWebhookSecret sets the signing secret (whsec_…) for Webhooks.Verify. Defaults to SMSWIRE_WEBHOOK_SECRET.
func WithWebhookSecret(secret string) Option { return func(c *config) { c.webhookSecret = secret } }

// WithMaxRetries sets how many times a call is retried (see the README for which calls). Default 2.
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

// WithMaxRetryWait sets the longest Retry-After worth waiting for; longer ones fail at once. Default 60 s.
func WithMaxRetryWait(d time.Duration) Option { return func(c *config) { c.maxRetryWait = d } }

// WithTimeout sets the per-attempt timeout. Default 30 s.
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithHTTPClient sets the http.Client requests go through (proxies, transports, tests).
func WithHTTPClient(hc *http.Client) Option { return func(c *config) { c.http = hc } }

// New returns a client for one project. Unset options fall back to SMSWIRE_KEY, SMSWIRE_PROJECT,
// SMSWIRE_URL and SMSWIRE_WEBHOOK_SECRET.
func New(opts ...Option) (*Client, error) {
	c := config{
		apiKey:        os.Getenv("SMSWIRE_KEY"),
		project:       os.Getenv("SMSWIRE_PROJECT"),
		baseURL:       os.Getenv("SMSWIRE_URL"),
		webhookSecret: os.Getenv("SMSWIRE_WEBHOOK_SECRET"),
		maxRetries:    2,
		maxRetryWait:  60 * time.Second,
		timeout:       30 * time.Second,
	}
	for _, opt := range opts {
		opt(&c)
	}
	if c.apiKey == "" {
		return nil, errors.New("smswire: pass WithAPIKey or set SMSWIRE_KEY")
	}
	if c.project == "" {
		return nil, errors.New("smswire: pass WithProject or set SMSWIRE_PROJECT")
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	if c.maxRetries < 0 {
		c.maxRetries = 0
	}
	client := &Client{
		apiKey:       c.apiKey,
		baseURL:      strings.TrimRight(c.baseURL, "/"),
		maxRetries:   c.maxRetries,
		maxRetryWait: c.maxRetryWait,
		timeout:      c.timeout,
		http:         c.http,
		userAgent:    "smswire-go/" + Version,
		sleep:        sleepCtx,
	}
	base := "/v1/p/" + url.PathEscape(c.project)
	client.Messages = &MessagesService{client: client, base: base + "/messages"}
	client.OTP = &OTPService{client: client, base: base + "/otp"}
	client.Schedules = &SchedulesService{client: client, base: base + "/schedules"}
	client.Webhooks = &WebhooksService{secret: c.webhookSecret}
	return client, nil
}

// Ptr returns a pointer to v, for the optional fields that need one (Enabled, MaxRuns, SendAt…).
func Ptr[T any](v T) *T { return &v }
