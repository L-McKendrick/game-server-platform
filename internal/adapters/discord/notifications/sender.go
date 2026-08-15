package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type SecretsAPI interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type Sender struct {
	secrets    SecretsAPI
	secretName string
	client     *http.Client
	apiBase    string
	mu         sync.Mutex
	token      string
}

func New(secrets SecretsAPI, secretName string) *Sender {
	return &Sender{
		secrets: secrets, secretName: strings.TrimSpace(secretName),
		client: &http.Client{Timeout: 10 * time.Second}, apiBase: "https://discord.com/api/v10",
	}
}

func (sender *Sender) Send(ctx context.Context, request domain.NotificationRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	token, err := sender.botToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"content":          request.Content,
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		return err
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		sender.apiBase+"/channels/"+request.ChannelID+"/messages",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Authorization", "Bot "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "game-server-platform-notifications/1")
	response, err := sender.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("send Discord notification: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Discord notification returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

// SendCard idempotently creates or edits a bot-authored session card. Discord
// enforces nonce uniqueness for creates and returns the existing message when a
// delivery retry uses the same nonce.
func (sender *Sender) SendCard(ctx context.Context, request domain.NotificationRequest, messageID string) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	if request.Kind != domain.NotificationSessionCard {
		return "", fmt.Errorf("notification is not a session card")
	}
	token, err := sender.botToken(ctx)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"content":          request.Content,
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
	method := http.MethodPatch
	endpoint := sender.apiBase + "/channels/" + request.ChannelID + "/messages/" + strings.TrimSpace(messageID)
	if strings.TrimSpace(messageID) == "" {
		method = http.MethodPost
		endpoint = sender.apiBase + "/channels/" + request.ChannelID + "/messages"
		digest := sha256.Sum256([]byte(request.SessionID))
		payload["nonce"] = fmt.Sprintf("%x", digest[:12])
		payload["enforce_nonce"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpRequest.Header.Set("Authorization", "Bot "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "game-server-platform-notifications/1")
	response, err := sender.client.Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("deliver Discord session card: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Discord session card returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	if readErr != nil {
		return "", fmt.Errorf("read Discord session card response: %w", readErr)
	}
	var message struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return "", fmt.Errorf("decode Discord session card response: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" {
		return "", fmt.Errorf("Discord session card response omitted message ID")
	}
	return strings.TrimSpace(message.ID), nil
}

func (sender *Sender) botToken(ctx context.Context) (string, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.token != "" {
		return sender.token, nil
	}
	if sender.secrets == nil || sender.secretName == "" {
		return "", fmt.Errorf("Discord notification secret is not configured")
	}
	result, err := sender.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(sender.secretName)})
	if err != nil {
		return "", fmt.Errorf("read Discord bot token: %w", err)
	}
	value := strings.TrimSpace(aws.ToString(result.SecretString))
	var object map[string]string
	if json.Unmarshal([]byte(value), &object) == nil && strings.TrimSpace(object["token"]) != "" {
		value = strings.TrimSpace(object["token"])
	}
	if value == "" {
		return "", fmt.Errorf("Discord bot token secret is empty")
	}
	sender.token = value
	return sender.token, nil
}
