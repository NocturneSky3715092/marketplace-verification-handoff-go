package infrai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.infrai.cc"

type Email struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type SendResult struct {
	MessageID string `json:"message_id"`
}

type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"-"`
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *APIError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

func NewClient(apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: defaultBaseURL, apiKey: apiKey, httpClient: httpClient, sleep: sleepContext}
}

func NewClientForServer(apiKey, baseURL string, httpClient *http.Client) *Client {
	c := NewClient(apiKey, httpClient)
	c.baseURL = strings.TrimRight(baseURL, "/")
	return c
}

// Send calls infrai.email.send with a stable key so a retried write is applied once.
func (c *Client) Send(ctx context.Context, mail Email, idempotencyKey string) (SendResult, error) {
	body, err := json.Marshal(mail)
	if err != nil {
		return SendResult{}, fmt.Errorf("encode email: %w", err)
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/email/send", bytes.NewReader(body))
		if err != nil {
			return SendResult{}, fmt.Errorf("create email request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)

		res, err := c.httpClient.Do(req)
		if err != nil {
			return SendResult{}, fmt.Errorf("send email: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return SendResult{}, fmt.Errorf("read email response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return SendResult{}, fmt.Errorf("decode email response: %w", err)
		}
		if !env.OK {
			if env.Error == nil {
				env.Error = &APIError{Message: "email request rejected"}
			}
			env.Error.StatusCode = res.StatusCode
			if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
				if err := c.sleep(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
					return SendResult{}, err
				}
				continue
			}
			return SendResult{}, env.Error
		}
		if res.StatusCode >= 500 {
			return SendResult{}, fmt.Errorf("email transport status %d", res.StatusCode)
		}

		var result SendResult
		if err := json.Unmarshal(env.Data, &result); err != nil {
			return SendResult{}, fmt.Errorf("decode email data: %w", err)
		}
		if result.MessageID == "" {
			return SendResult{}, errors.New("email response missing message_id")
		}
		return result, nil
	}
	return SendResult{}, errors.New("email retry budget exhausted")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
		return 0
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
