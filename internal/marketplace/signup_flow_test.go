package marketplace

import (
	"context"
	"testing"
	"time"

	"github.com/example/marketplace-verification-handoff/internal/infrai"
)

type capturedSender struct {
	mail infrai.Email
	key  string
}

func (s *capturedSender) Send(_ context.Context, mail infrai.Email, key string) (infrai.SendResult, error) {
	s.mail, s.key = mail, key
	return infrai.SendResult{MessageID: "msg_42"}, nil
}

func TestVerificationControlsOrderHandoff(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantError bool
		wantOrder string
	}{
		{name: "matching token releases handoff", token: "fixed-token", wantOrder: "ready_for_handoff"},
		{name: "different token keeps handoff closed", token: "other-token", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &capturedSender{}
			flow := NewFlow(sender, "http://localhost:8080")
			flow.newToken = func() (string, error) { return "fixed-token", nil }
			flow.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
			input := Signup{SignupID: "signup-42", SellerName: "Ada", SellerEmail: "seller@example.com", BuyerEmail: "buyer@example.com", AssetID: "asset-7", AssetName: "Ledger export", OrderID: "order-9"}

			started, err := flow.Start(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if started.Status != "verification_pending" || sender.key != "marketplace-signup-signup-42" {
				t.Fatalf("unexpected signup result: %#v, key %q", started, sender.key)
			}

			got, err := flow.Verify(tt.token)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected verification rejection")
				}
				if _, visible := flow.Order(input.OrderID); visible {
					t.Fatal("unverified order became visible for handoff")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Order.Status != tt.wantOrder || got.Assets[0].Status != "active" || got.BuyerUpdates[0].Kind != "seller_email_verified" {
				t.Fatalf("unexpected verified state: %#v", got)
			}
		})
	}
}
