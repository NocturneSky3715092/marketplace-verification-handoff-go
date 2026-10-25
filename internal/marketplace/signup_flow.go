package marketplace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"sync"
	"time"

	"github.com/example/marketplace-verification-handoff/internal/infrai"
)

var ErrInvalidVerification = errors.New("verification token is invalid or expired")

type EmailSender interface {
	Send(context.Context, infrai.Email, string) (infrai.SendResult, error)
}

type Signup struct {
	SignupID    string `json:"signup_id"`
	SellerName  string `json:"seller_name"`
	SellerEmail string `json:"seller_email"`
	BuyerEmail  string `json:"buyer_email"`
	AssetID     string `json:"asset_id"`
	AssetName   string `json:"asset_name"`
	OrderID     string `json:"order_id"`
}

type SellerAsset struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type BuyerUpdate struct {
	BuyerEmail string `json:"buyer_email"`
	Kind       string `json:"kind"`
}

type OrderHandoff struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

type VerificationResult struct {
	SellerEmail  string        `json:"seller_email"`
	Assets       []SellerAsset `json:"assets"`
	BuyerUpdates []BuyerUpdate `json:"buyer_updates"`
	Order        OrderHandoff  `json:"order"`
}

type SignupResult struct {
	SignupID  string `json:"signup_id"`
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

type record struct {
	signup    Signup
	tokenHash [32]byte
	expiresAt time.Time
	messageID string
	verified  bool
}

type Flow struct {
	mu       sync.Mutex
	sender   EmailSender
	baseURL  string
	now      func() time.Time
	newToken func() (string, error)
	records  map[string]*record
}

func NewFlow(sender EmailSender, publicBaseURL string) *Flow {
	return &Flow{sender: sender, baseURL: publicBaseURL, now: time.Now, newToken: randomToken, records: make(map[string]*record)}
}

func (f *Flow) Start(ctx context.Context, input Signup) (SignupResult, error) {
	if err := validateSignup(input); err != nil {
		return SignupResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.records[input.SignupID]; existing != nil {
		return SignupResult{SignupID: input.SignupID, MessageID: existing.messageID, Status: "verification_pending"}, nil
	}

	token, err := f.newToken()
	if err != nil {
		return SignupResult{}, fmt.Errorf("create verification token: %w", err)
	}
	link := f.baseURL + "/verify?token=" + token
	mail := infrai.Email{
		To:      input.SellerEmail,
		Subject: "Verify your seller email",
		HTML:    "<p>Hello " + html.EscapeString(input.SellerName) + ",</p><p><a href=\"" + html.EscapeString(link) + "\">Verify email and release order handoff</a></p>",
	}
	sent, err := f.sender.Send(ctx, mail, "marketplace-signup-"+input.SignupID)
	if err != nil {
		return SignupResult{}, err
	}
	f.records[input.SignupID] = &record{signup: input, tokenHash: sha256.Sum256([]byte(token)), expiresAt: f.now().Add(30 * time.Minute), messageID: sent.MessageID}
	return SignupResult{SignupID: input.SignupID, MessageID: sent.MessageID, Status: "verification_pending"}, nil
}

func (f *Flow) Verify(token string) (VerificationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hash := sha256.Sum256([]byte(token))
	for _, entry := range f.records {
		if entry.tokenHash != hash || f.now().After(entry.expiresAt) {
			continue
		}
		entry.verified = true
		return stateFor(entry), nil
	}
	return VerificationResult{}, ErrInvalidVerification
}

func (f *Flow) Order(orderID string) (VerificationResult, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, entry := range f.records {
		if entry.signup.OrderID == orderID && entry.verified {
			return stateFor(entry), true
		}
	}
	return VerificationResult{}, false
}

func stateFor(entry *record) VerificationResult {
	return VerificationResult{
		SellerEmail:  entry.signup.SellerEmail,
		Assets:       []SellerAsset{{ID: entry.signup.AssetID, Name: entry.signup.AssetName, Status: "active"}},
		BuyerUpdates: []BuyerUpdate{{BuyerEmail: entry.signup.BuyerEmail, Kind: "seller_email_verified"}},
		Order:        OrderHandoff{OrderID: entry.signup.OrderID, Status: "ready_for_handoff"},
	}
}

func validateSignup(input Signup) error {
	if input.SignupID == "" || input.SellerName == "" || input.SellerEmail == "" || input.BuyerEmail == "" || input.AssetID == "" || input.AssetName == "" || input.OrderID == "" {
		return errors.New("all signup fields are required")
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
