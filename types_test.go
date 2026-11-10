package smswire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// jsonFields lists a struct's JSON field names.
func jsonFields(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// The structs name the same fields as the API's OpenAPI document (sdks/openapi.json).
func TestTypesMatchTheSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "openapi.json"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("sdks/openapi.json is only in the SMSWire monorepo")
	}
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	pairs := map[string]any{
		"SendMessageRequest":    SendMessageParams{},
		"MessageView":           Message{},
		"MessagePage":           MessagePage{},
		"BulkItem":              BulkItem{},
		"BulkSendRequest":       SendBulkParams{},
		"BulkSendResponse":      BulkResult{},
		"RejectedItem":          RejectedItem{},
		"OtpRequest":            SendOTPParams{},
		"OtpResponse":           OTP{},
		"OtpVerifyResponse":     OTPVerification{},
		"ScheduleRecipient":     ScheduleRecipient{},
		"ScheduleRequest":       CreateScheduleParams{},
		"ScheduleUpdate":        UpdateScheduleParams{},
		"ScheduleView":          Schedule{},
		"ScheduleRunView":       ScheduleRun{},
		"SchedulePage":          SchedulePage{},
		"StatusWebhookPayload":  StatusEvent{},
		"InboundWebhookPayload": ReceivedEvent{},
		"AlertWebhookPayload":   AlertEvent{},
	}
	for schema, v := range pairs {
		s, ok := spec.Components.Schemas[schema]
		if !ok {
			t.Errorf("no %s in the spec", schema)
			continue
		}
		var want []string
		for k := range s.Properties {
			want = append(want, k)
		}
		sort.Strings(want)
		if got := jsonFields(reflect.TypeOf(v)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: fields %v, spec has %v", schema, got, want)
		}
	}
}
