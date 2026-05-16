package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/abhinavxd/libredesk/internal/attachment"
	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/envelope"
	"github.com/abhinavxd/libredesk/internal/inbox"
	"github.com/abhinavxd/libredesk/internal/inbox/channel/whatsapp"
	"github.com/valyala/fasthttp"
	"github.com/volatiletech/null/v9"
	"github.com/zerodha/fastglue"
)

// handleTwilioWhatsAppWebhook receives inbound WhatsApp messages forwarded by Twilio.
// The route is public (no auth middleware) but protected by Twilio signature validation.
// Twilio expects a 200 OK with a TwiML <Response/> body (empty response suppresses any reply).
func handleTwilioWhatsAppWebhook(r *fastglue.Request) error {
	var (
		app      = r.Context.(*App)
		inboxUUID = r.RequestCtx.UserValue("uuid").(string)
	)

	// Parse application/x-www-form-urlencoded body.
	rawBody := string(r.RequestCtx.PostBody())
	params, err := url.ParseQuery(rawBody)
	if err != nil {
		app.lo.Error("whatsapp webhook: failed to parse form body", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "invalid request body", nil, envelope.InputError)
	}

	// Fetch the inbox DB record (decrypts config).
	inboxRecord, err := app.inbox.GetDBRecord(inboxUUID)
	if err != nil {
		app.lo.Error("whatsapp webhook: inbox not found", "uuid", inboxUUID, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, app.i18n.T("validation.notFoundInbox"), nil, envelope.InputError)
	}

	if inboxRecord.Channel != whatsapp.ChannelWhatsApp {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "inbox is not a whatsapp channel", nil, envelope.InputError)
	}

	// Unmarshal the WhatsApp channel config.
	var cfg whatsapp.Config
	if err := json.Unmarshal(inboxRecord.Config, &cfg); err != nil {
		app.lo.Error("whatsapp webhook: failed to unmarshal config", "inbox_uuid", inboxUUID, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}

	// Validate Twilio signature.
	rootURL, _ := app.setting.GetAppRootURL()
	webhookPath := fmt.Sprintf("/api/v1/inboxes/whatsapp/%s/webhook", inboxUUID)
	webhookURL := strings.TrimRight(rootURL, "/") + webhookPath
	signature := string(r.RequestCtx.Request.Header.Peek("X-Twilio-Signature"))

	if !whatsapp.ValidateSignature(cfg.AuthToken, webhookURL, params, signature) {
		app.lo.Warn("whatsapp webhook: invalid twilio signature", "inbox_uuid", inboxUUID)
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "invalid signature", nil, envelope.InputError)
	}

	// Extract message fields from Twilio params.
	messageSID := params.Get("MessageSid")
	from := params.Get("From") // "whatsapp:+1234567890"
	body := params.Get("Body")

	if messageSID == "" || from == "" {
		app.lo.Warn("whatsapp webhook: missing required params", "inbox_uuid", inboxUUID, "sid", messageSID, "from", from)
		return twilioEmptyResponse(r)
	}

	// Skip duplicate messages.
	exists, err := app.conversation.MessageExists(messageSID)
	if err != nil {
		app.lo.Error("whatsapp webhook: checking message existence", "sid", messageSID, "error", err)
	}
	if exists {
		return twilioEmptyResponse(r)
	}

	// Build contact from the Twilio params.
	contactUser := whatsapp.ContactFromTwilioWebhook(params)

	// Build the incoming message.
	msg := cmodels.IncomingMessage{
		Channel:     whatsapp.ChannelWhatsApp,
		InboxID:     inboxRecord.ID,
		SourceID:    null.StringFrom(messageSID),
		Content:     body,
		ContentType: cmodels.ContentTypeText,
		Contact: cmodels.IncomingContact{
			FirstName:   contactUser.FirstName,
			LastName:    contactUser.LastName,
			Email:       contactUser.Email,
			PhoneNumber: contactUser.PhoneNumber,
		},
	}

	// Download any media attachments Twilio is hosting (MediaUrl0, MediaUrl1, …).
	numMedia, _ := strconv.Atoi(params.Get("NumMedia"))
	if numMedia > 0 {
		twilioHTTP := &http.Client{Timeout: 30 * time.Second}
		for i := 0; i < numMedia; i++ {
			mediaURL := params.Get(fmt.Sprintf("MediaUrl%d", i))
			if mediaURL == "" {
				continue
			}
			content, contentType, err := downloadTwilioMedia(twilioHTTP, cfg.AccountSID, cfg.AuthToken, mediaURL)
			if err != nil {
				app.lo.Error("whatsapp webhook: failed to download media, skipping", "url", mediaURL, "error", err)
				continue
			}
			ext := extensionForMIME(contentType)
			filename := path.Base(mediaURL) + ext
			msg.Attachments = append(msg.Attachments, attachment.Attachment{
				Name:        filename,
				Content:     content,
				ContentType: contentType,
				Size:        len(content),
				Disposition: attachment.DispositionAttachment,
			})
		}
	}

	// Handle location messages (Latitude/Longitude present, Body may be empty).
	latitude := params.Get("Latitude")
	longitude := params.Get("Longitude")
	if latitude != "" && longitude != "" {
		label := params.Get("Label")
		address := params.Get("Address")
		mapsURL := fmt.Sprintf("https://www.google.com/maps?q=%s,%s", latitude, longitude)
		var title string
		if label != "" {
			title = html.EscapeString(label)
		} else {
			title = "Location"
		}
		var addrLine string
		if address != "" {
			addrLine = fmt.Sprintf(`<div style="font-size:0.85em;color:#555;margin-top:2px">%s</div>`, html.EscapeString(address))
		}
		coordsLine := fmt.Sprintf(`<div style="font-size:0.8em;color:#888;margin-top:2px">%s, %s</div>`, html.EscapeString(latitude), html.EscapeString(longitude))
		locationHTML := fmt.Sprintf(
			`<div style="display:inline-block;border:1px solid #e2e8f0;border-radius:10px;padding:10px 14px;max-width:300px;background:#f8fafc">`+
				`<div style="font-weight:600;color:#111">📍 %s</div>`+
				`%s%s`+
				`<div style="margin-top:8px"><a href="%s" target="_blank" rel="noopener noreferrer" style="font-size:0.85em;color:#2563eb">View on Google Maps →</a></div>`+
				`</div>`,
			title, addrLine, coordsLine, mapsURL,
		)
		if msg.Content != "" {
			// Wrap any preceding text as HTML too.
			msg.Content = fmt.Sprintf("<div>%s</div>%s", html.EscapeString(msg.Content), locationHTML)
		} else {
			msg.Content = locationHTML
		}
		msg.ContentType = cmodels.ContentTypeHTML
	}

	// Skip if there's nothing to store.
	if strings.TrimSpace(msg.Content) == "" && len(msg.Attachments) == 0 {
		app.lo.Warn("whatsapp webhook: empty message content, skipping", "sid", messageSID)
		return twilioEmptyResponse(r)
	}

	if err := app.conversation.EnqueueIncoming(msg); err != nil {
		app.lo.Error("whatsapp webhook: failed to enqueue incoming message", "sid", messageSID, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}

	app.lo.Info("whatsapp webhook: enqueued incoming message", "inbox_uuid", inboxUUID, "sid", messageSID)
	return twilioEmptyResponse(r)
}

// downloadTwilioMedia fetches a Twilio-hosted media file using Basic Auth.
func downloadTwilioMedia(client *http.Client, accountSID, authToken, mediaURL string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.SetBasicAuth(accountSID, authToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("twilio media fetch returned status %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if ct, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = ct
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 25*1024*1024)) // 25 MB cap
	return body, contentType, err
}

// extensionForMIME returns a file extension (with leading dot) for common MIME types.
func extensionForMIME(contentType string) string {
	exts, err := mime.ExtensionsByType(contentType)
	if err == nil && len(exts) > 0 {
		return exts[0]
	}
	// Fallback map for types the stdlib doesn't know.
	m := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"audio/ogg":       ".ogg",
		"audio/mpeg":      ".mp3",
		"audio/mp4":       ".mp4",
		"video/mp4":       ".mp4",
		"application/pdf": ".pdf",
	}
	if ext, ok := m[contentType]; ok {
		return ext
	}
	return ""
}

// twilioEmptyResponse returns an empty TwiML response to Twilio.
// This acknowledges receipt without sending any reply to the user.
func twilioEmptyResponse(r *fastglue.Request) error {
	r.RequestCtx.SetContentType("text/xml; charset=utf-8")
	r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
	r.RequestCtx.SetBodyString(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`)
	return nil
}

// handleSendWhatsAppReEngagement sends the configured re-engagement template for a
// WhatsApp conversation, allowing agents to proactively re-open the 24-hour service window.
func handleSendWhatsAppReEngagement(r *fastglue.Request) error {
	var (
		app  = r.Context.(*App)
		uuid = r.RequestCtx.UserValue("uuid").(string)
	)

	// Fetch conversation to get inbox info and contact.
	conv, err := app.conversation.GetConversation(0, uuid, "")
	if err != nil {
		return sendErrorEnvelope(r, err)
	}

	if conv.InboxChannel != inbox.ChannelWhatsApp {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "conversation is not a WhatsApp conversation", nil, envelope.InputError)
	}

	// Get the inbox instance.
	inb, err := app.inbox.Get(conv.InboxID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}

	tmpl, ok := inb.(inbox.TemplateMessenger)
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "inbox does not support template messages", nil, envelope.InputError)
	}

	waInb, ok := inb.(*whatsapp.WhatsApp)
	if !ok || !waInb.HasTemplate() {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "no re-engagement template configured for this inbox", nil, envelope.InputError)
	}

	// Resolve recipient phone from contact pseudo-email.
	contact, err := app.user.GetContactOrVisitor(conv.ContactID, "")
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}
	toNumber, err := whatsapp.PhoneFromPseudoEmail(contact.Email.String)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "could not resolve WhatsApp number for contact", nil, envelope.InputError)
	}

	cfg := waInb.GetConfig()
	if err := tmpl.SendTemplate(toNumber, cfg.ContentSID); err != nil {
		app.lo.Error("whatsapp re-engagement: failed to send template", "conversation_uuid", uuid, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}

	app.lo.Info("whatsapp re-engagement template sent", "conversation_uuid", uuid)
	return r.SendEnvelope(true)
}
