// Package whatsapp implements a WhatsApp inbox via the Twilio API.
// Inbound messages arrive through a Twilio webhook (POST); outbound messages
// are sent via the Twilio Messages REST API.
package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/inbox"
	umodels "github.com/abhinavxd/libredesk/internal/user/models"
	"github.com/volatiletech/null/v9"
	"github.com/zerodha/logf"
)

const (
	ChannelWhatsApp = "whatsapp"

	// PhoneSuffix is appended to phone numbers to form a pseudo-email used as the
	// contact's unique identifier in the users table.
	PhoneSuffix = "@wa.phone"

	twilioAPIBase = "https://api.twilio.com/2010-04-01/Accounts"
)

// Config holds the WhatsApp inbox configuration.
type Config struct {
	AccountSID string `json:"account_sid"`
	AuthToken  string `json:"auth_token"`
	// FromNumber is the Twilio WhatsApp-enabled number in E.164 format, e.g. "+14155238886".
	FromNumber string `json:"from_number"`
}

// WhatsApp implements inbox.Inbox for a Twilio-backed WhatsApp channel.
type WhatsApp struct {
	id           int
	config       Config
	lo           *logf.Logger
	messageStore inbox.MessageStore
	userStore    inbox.UserStore
	httpClient   *http.Client
}

// Opts holds the options for creating a new WhatsApp inbox.
type Opts struct {
	ID     int
	Config Config
	Lo     *logf.Logger
}

// New returns a new WhatsApp inbox instance.
func New(msgStore inbox.MessageStore, userStore inbox.UserStore, opts Opts) (*WhatsApp, error) {
	if opts.Config.AccountSID == "" {
		return nil, fmt.Errorf("whatsapp inbox: account_sid is required")
	}
	if opts.Config.AuthToken == "" {
		return nil, fmt.Errorf("whatsapp inbox: auth_token is required")
	}
	if opts.Config.FromNumber == "" {
		return nil, fmt.Errorf("whatsapp inbox: from_number is required")
	}

	return &WhatsApp{
		id:           opts.ID,
		config:       opts.Config,
		lo:           opts.Lo,
		messageStore: msgStore,
		userStore:    userStore,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Identifier returns the database ID of this inbox.
func (w *WhatsApp) Identifier() int {
	return w.id
}

// Channel returns the channel type string.
func (w *WhatsApp) Channel() string {
	return ChannelWhatsApp
}

// FromAddress returns the Twilio WhatsApp sender number.
func (w *WhatsApp) FromAddress() string {
	return w.config.FromNumber
}

// ReplyToAddress is not applicable to WhatsApp.
func (w *WhatsApp) ReplyToAddress() string {
	return ""
}

// Receive is a no-op. Inbound messages arrive via the Twilio webhook handler.
func (w *WhatsApp) Receive(_ context.Context) error {
	return nil
}

// Send sends an outbound message to the contact via Twilio's WhatsApp API.
// The recipient's phone number is derived from their stored pseudo-email
// (format: "+1234567890@wa.phone").
func (w *WhatsApp) Send(msg models.OutboundMessage) error {
	if msg.MessageReceiverID <= 0 {
		w.lo.Warn("whatsapp send: empty message receiver id", "message_uuid", msg.UUID)
		return nil
	}

	// Look up contact to get their WhatsApp phone number.
	contact, err := w.userStore.GetContactOrVisitor(msg.MessageReceiverID, "")
	if err != nil {
		return fmt.Errorf("whatsapp send: looking up contact (id=%d): %w", msg.MessageReceiverID, err)
	}

	toNumber, err := PhoneFromPseudoEmail(contact.Email.String)
	if err != nil {
		return fmt.Errorf("whatsapp send: resolving recipient phone for contact %d: %w", msg.MessageReceiverID, err)
	}

	// Use plain text; Twilio WhatsApp does not render HTML.
	body := strings.TrimSpace(msg.TextContent)
	if body == "" {
		body = strings.TrimSpace(msg.Content)
	}

	// Collect public attachment URLs for Twilio MediaUrl params.
	var mediaURLs []string
	for _, a := range msg.Attachments {
		if a.URL != "" {
			mediaURLs = append(mediaURLs, a.URL)
		}
	}

	if body == "" && len(mediaURLs) == 0 {
		w.lo.Warn("whatsapp send: empty body and no media, skipping", "message_uuid", msg.UUID)
		return nil
	}

	return w.sendViaTwilio(toNumber, body, mediaURLs)
}

// Close is a no-op; there are no persistent connections to close.
func (w *WhatsApp) Close() error {
	return nil
}

// GetConfig returns a copy of the inbox configuration (used for signature validation in the webhook handler).
func (w *WhatsApp) GetConfig() Config {
	return w.config
}

// ValidateSignature validates the X-Twilio-Signature header against the request
// URL and POST parameters.  See https://www.twilio.com/docs/usage/webhooks/webhooks-security
func ValidateSignature(authToken, webhookURL string, params url.Values, signature string) bool {
	// Build the string to sign: URL + sorted param key-value pairs concatenated.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(webhookURL)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(params.Get(k))
	}

	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(sb.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// PseudoEmailFromPhone converts a WhatsApp phone number from Twilio format
// ("whatsapp:+1234567890") or plain E.164 format ("+1234567890") to the
// pseudo-email used as the contact identifier in LibreDesk.
func PseudoEmailFromPhone(twilioFrom string) string {
	phone := strings.TrimPrefix(twilioFrom, "whatsapp:")
	phone = strings.TrimSpace(phone)
	return phone + PhoneSuffix
}

// PhoneFromPseudoEmail extracts the E.164 phone number from a pseudo-email
// and returns it in Twilio WhatsApp format ("whatsapp:+1234567890").
func PhoneFromPseudoEmail(pseudoEmail string) (string, error) {
	if !strings.HasSuffix(pseudoEmail, PhoneSuffix) {
		return "", fmt.Errorf("not a whatsapp pseudo-email: %q", pseudoEmail)
	}
	phone := strings.TrimSuffix(pseudoEmail, PhoneSuffix)
	if phone == "" {
		return "", fmt.Errorf("empty phone number in pseudo-email: %q", pseudoEmail)
	}
	return "whatsapp:" + phone, nil
}

// sendViaTwilio sends a WhatsApp message via the Twilio Messages REST API.
// toNumber must be in Twilio format, e.g. "whatsapp:+1234567890".
// mediaURLs are optional publicly accessible attachment URLs.
func (w *WhatsApp) sendViaTwilio(toNumber, body string, mediaURLs []string) error {
	apiURL := fmt.Sprintf("%s/%s/Messages.json", twilioAPIBase, w.config.AccountSID)

	form := url.Values{}
	form.Set("From", "whatsapp:"+w.config.FromNumber)
	form.Set("To", toNumber)
	if body != "" {
		form.Set("Body", body)
	}
	for _, u := range mediaURLs {
		form.Add("MediaUrl", u)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("creating twilio request: %w", err)
	}
	req.SetBasicAuth(w.config.AccountSID, w.config.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling twilio api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("twilio api error (status %d): %s", resp.StatusCode, string(body))
	}

	w.lo.Info("whatsapp message sent via twilio", "to", toNumber, "status", resp.StatusCode)
	return nil
}

// ContactFromTwilioWebhook extracts a contact record from Twilio webhook params.
// "From" is e.g. "whatsapp:+1234567890"; "ProfileName" is the sender's display name.
func ContactFromTwilioWebhook(params url.Values) umodels.User {
	from := params.Get("From") // e.g. "whatsapp:+1234567890"
	phone := strings.TrimPrefix(from, "whatsapp:")
	phone = strings.TrimSpace(phone)

	profileName := strings.TrimSpace(params.Get("ProfileName"))
	firstName := phone
	lastName := ""
	if profileName != "" {
		parts := strings.SplitN(profileName, " ", 2)
		firstName = parts[0]
		if len(parts) > 1 {
			lastName = parts[1]
		}
	}

	return umodels.User{
		FirstName:   firstName,
		LastName:    lastName,
		Email:       null.StringFrom(phone + PhoneSuffix),
		PhoneNumber: null.StringFrom(phone),
	}
}
