package smswire_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	smswire "github.com/jolabs-io/smswire-go"
)

func Example() {
	sms, err := smswire.New() // SMSWIRE_KEY, SMSWIRE_PROJECT, SMSWIRE_URL
	if err != nil {
		log.Fatal(err)
	}
	msg, err := sms.Messages.Send(context.Background(), &smswire.SendMessageParams{
		To:   "+14155550123",
		Body: "Your order A-1042 has shipped",
		Type: smswire.TypeTransactional,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(msg.ID, msg.Status)
}

func ExampleOTPService_Verify() {
	sms, _ := smswire.New()
	ctx := context.Background()
	otp, err := sms.OTP.Send(ctx, &smswire.SendOTPParams{To: "+14155550123"})
	if err != nil {
		log.Fatal(err)
	}
	result, err := sms.OTP.Verify(ctx, otp.ID, "482913")
	if err != nil {
		log.Fatal(err)
	}
	if !result.Valid {
		fmt.Println("rejected:", *result.Reason)
	}
}

func ExampleVerifyWebhook() {
	secret := "whsec_…" // the project's signing secret
	http.HandleFunc("POST /hooks/smswire", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		event, err := smswire.VerifyWebhook(body, r.Header.Get(smswire.SignatureHeader), secret)
		if errors.Is(err, smswire.ErrInvalidSignature) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if event.Type == smswire.EventMessageStatus {
			fmt.Println(event.Status.ID, event.Status.Status)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func ExampleError() {
	sms, _ := smswire.New()
	_, err := sms.Messages.Get(context.Background(), "msg_missing")
	var apiErr *smswire.Error
	if errors.As(err, &apiErr) {
		fmt.Println(apiErr.Status, apiErr.Code) // 404 message_not_found
	}
}
