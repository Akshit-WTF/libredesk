package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/envelope"
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

	// Attach any media URLs as plain text references (Twilio sends MediaUrl0, MediaUrl1, …).
	numMedia, _ := strconv.Atoi(params.Get("NumMedia"))
	if numMedia > 0 {
		var mediaLines []string
		for i := 0; i < numMedia; i++ {
			mediaURL := params.Get(fmt.Sprintf("MediaUrl%d", i))
			if mediaURL != "" {
				mediaLines = append(mediaLines, mediaURL)
			}
		}
		if len(mediaLines) > 0 {
			if msg.Content != "" {
				msg.Content += "\n"
			}
			msg.Content += strings.Join(mediaLines, "\n")
		}
	}

	if err := app.conversation.EnqueueIncoming(msg); err != nil {
		app.lo.Error("whatsapp webhook: failed to enqueue incoming message", "sid", messageSID, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, app.i18n.T("globals.messages.somethingWentWrong"), nil, envelope.GeneralError)
	}

	app.lo.Info("whatsapp webhook: enqueued incoming message", "inbox_uuid", inboxUUID, "sid", messageSID)
	return twilioEmptyResponse(r)
}

// twilioEmptyResponse returns an empty TwiML response to Twilio.
// This acknowledges receipt without sending any reply to the user.
func twilioEmptyResponse(r *fastglue.Request) error {
	r.RequestCtx.SetContentType("text/xml; charset=utf-8")
	r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
	r.RequestCtx.SetBodyString(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`)
	return nil
}
