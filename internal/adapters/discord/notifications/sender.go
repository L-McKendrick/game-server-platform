package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/componentid"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
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

// DeleteMessage removes a known bot-owned session message. Discord's not-found
// response is idempotent success; no channel history is enumerated.
func (sender *Sender) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	channelID, messageID = strings.TrimSpace(channelID), strings.TrimSpace(messageID)
	if channelID == "" || messageID == "" {
		return fmt.Errorf("Discord channel and message IDs are required")
	}
	token, err := sender.botToken(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, sender.apiBase+"/channels/"+channelID+"/messages/"+messageID, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bot "+token)
	request.Header.Set("User-Agent", "game-server-platform-reset/1")
	response, err := sender.client.Do(request)
	if err != nil {
		return fmt.Errorf("delete Discord session message: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("delete Discord session message returned %s", response.Status)
	}
	return nil
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

// SendCard creates or edits a bot-authored session card through the channel
// message API and a bot token, independently of interaction webhook tokens.
// The enforced nonce protects an ambiguous create retry during Discord's
// documented short deduplication window; durable edit idempotency is owned by
// the notification worker's persisted delivery reference.
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
	payload := map[string]any{"allowed_mentions": map[string]any{"parse": []string{}}}
	if request.Embed != nil {
		payload["content"] = ""
		payload["embeds"] = []domain.NotificationEmbed{*request.Embed}
	} else {
		payload["content"] = request.Content
	}
	components, err := sessionCardControls(request)
	if err != nil {
		return "", err
	}
	if len(components) > 0 {
		payload["components"] = components
	}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		result, status, sendErr := sender.sendCardAttempt(ctx, token, request, payload, http.MethodPatch, messageID)
		if sendErr == nil {
			return result, nil
		}
		if status != http.StatusNotFound {
			return "", sendErr
		}
	}
	result, _, err := sender.sendCardAttempt(ctx, token, request, payload, http.MethodPost, "")
	return result, err
}

func (sender *Sender) sendCardAttempt(
	ctx context.Context,
	token string,
	request domain.NotificationRequest,
	payload map[string]any,
	method string,
	messageID string,
) (string, int, error) {
	result, status, err := sender.sendCardRequest(ctx, token, request, payload, method, messageID)
	if err == nil || request.Embed == nil || status != http.StatusForbidden {
		return result, status, err
	}
	fallback := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		fallback[key] = value
	}
	fallback["content"] = request.Content
	fallback["embeds"] = []domain.NotificationEmbed{}
	return sender.sendCardRequest(ctx, token, request, fallback, method, messageID)
}

func (sender *Sender) sendCardRequest(
	ctx context.Context,
	token string,
	request domain.NotificationRequest,
	basePayload map[string]any,
	method string,
	messageID string,
) (string, int, error) {
	payload := make(map[string]any, len(basePayload)+2)
	for key, value := range basePayload {
		payload[key] = value
	}
	endpoint := sender.apiBase + "/channels/" + request.ChannelID + "/messages/" + messageID
	if method == http.MethodPost {
		endpoint = sender.apiBase + "/channels/" + request.ChannelID + "/messages"
		payload["nonce"] = notificationNonce("card", request)
		payload["enforce_nonce"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	httpRequest.Header.Set("Authorization", "Bot "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "game-server-platform-notifications/1")
	response, err := sender.client.Do(httpRequest)
	if err != nil {
		return "", 0, fmt.Errorf("deliver Discord session card: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", response.StatusCode, fmt.Errorf("Discord session card returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	if readErr != nil {
		return "", response.StatusCode, fmt.Errorf("read Discord session card response: %w", readErr)
	}
	var message struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return "", response.StatusCode, fmt.Errorf("decode Discord session card response: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" {
		return "", response.StatusCode, fmt.Errorf("Discord session card response omitted message ID")
	}
	return strings.TrimSpace(message.ID), response.StatusCode, nil
}

func sessionCardControls(request domain.NotificationRequest) ([]map[string]any, error) {
	if request.CardRevision < 1 {
		return nil, nil
	}
	token := sessioncard.ControlToken(request.SessionID)
	if token == "" {
		return nil, nil
	}
	revision := uint64(request.CardRevision)
	type control struct {
		action string
		label  string
		style  int
	}
	controls := []control{
		{componentid.ActionShowPlayers, "Show players", 1},
		{componentid.ActionRefresh, "Refresh", 2},
	}
	buttons := make([]map[string]any, 0, len(controls))
	for _, control := range controls {
		customID, customIDErr := componentid.New(control.action, revision, token)
		if customIDErr != nil {
			return nil, fmt.Errorf("build session card control: %w", customIDErr)
		}
		buttons = append(buttons, map[string]any{
			"type": 2, "style": control.style, "label": control.label, "custom_id": customID,
		})
	}
	return []map[string]any{{"type": 1, "components": buttons}}, nil
}

// SendModlist edits the stable attachment message when it exists. A 404 means
// the bot-authored message was deleted, so the same durable S3 body is posted
// as a replacement and its new ID is returned for persistence.
func (sender *Sender) SendModlist(ctx context.Context, request domain.NotificationRequest, body []byte, messageID string) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	if request.Kind != domain.NotificationSessionModlist || request.Attachment == nil {
		return "", fmt.Errorf("notification is not a session modlist")
	}
	if int64(len(body)) != request.Attachment.SizeBytes {
		return "", fmt.Errorf("modlist body size does not match notification metadata")
	}
	digest := sha256.Sum256(body)
	if fmt.Sprintf("%x", digest[:]) != request.Attachment.SHA256 {
		return "", fmt.Errorf("modlist body checksum does not match notification metadata")
	}
	token, err := sender.botToken(ctx)
	if err != nil {
		return "", err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		result, status, sendErr := sender.sendModlistRequest(ctx, token, request, body, http.MethodPatch, messageID)
		if sendErr == nil {
			return result, nil
		}
		if status != http.StatusNotFound {
			return "", sendErr
		}
	}
	result, _, err := sender.sendModlistRequest(ctx, token, request, body, http.MethodPost, "")
	return result, err
}

func (sender *Sender) sendModlistRequest(ctx context.Context, token string, request domain.NotificationRequest, body []byte, method, messageID string) (string, int, error) {
	payload := map[string]any{
		"content":          request.Content,
		"allowed_mentions": map[string]any{"parse": []string{}},
		"attachments": []map[string]any{{
			"id": 0, "filename": request.Attachment.Filename,
			"description": "Sanitized Arma 3 Launcher modlist",
		}},
	}
	endpoint := sender.apiBase + "/channels/" + request.ChannelID + "/messages/" + messageID
	if method == http.MethodPost {
		endpoint = sender.apiBase + "/channels/" + request.ChannelID + "/messages"
		payload["nonce"] = notificationNonce("modlist", request)
		payload["enforce_nonce"] = true
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	payloadPart, err := writer.CreateFormField("payload_json")
	if err != nil {
		return "", 0, err
	}
	if _, err := payloadPart.Write(payloadJSON); err != nil {
		return "", 0, err
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[0]"; filename="%s"`, request.Attachment.Filename))
	header.Set("Content-Type", request.Attachment.ContentType)
	filePart, err := writer.CreatePart(header)
	if err != nil {
		return "", 0, err
	}
	if _, err := filePart.Write(body); err != nil {
		return "", 0, err
	}
	if err := writer.Close(); err != nil {
		return "", 0, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded.Bytes()))
	if err != nil {
		return "", 0, err
	}
	httpRequest.Header.Set("Authorization", "Bot "+token)
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	httpRequest.Header.Set("User-Agent", "game-server-platform-notifications/1")
	response, err := sender.client.Do(httpRequest)
	if err != nil {
		return "", 0, fmt.Errorf("deliver Discord session modlist: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", response.StatusCode, fmt.Errorf("Discord session modlist returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	if readErr != nil {
		return "", response.StatusCode, fmt.Errorf("read Discord session modlist response: %w", readErr)
	}
	var message struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return "", response.StatusCode, fmt.Errorf("decode Discord session modlist response: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" {
		return "", response.StatusCode, fmt.Errorf("Discord session modlist response omitted message ID")
	}
	return strings.TrimSpace(message.ID), response.StatusCode, nil
}

func notificationNonce(kind string, request domain.NotificationRequest) string {
	digest := sha256.Sum256([]byte(kind + ":" + request.SessionID + ":" + request.NotificationID))
	return fmt.Sprintf("%x", digest[:12])
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
