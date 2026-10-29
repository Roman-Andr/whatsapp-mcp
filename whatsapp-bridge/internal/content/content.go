package content

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Unwrap returns the message that actually carries the content.
//
// View-once, ephemeral and document-with-caption messages are envelopes: the
// real Message sits one level down. Live events are already unwrapped by
// whatsmeow, but history sync payloads are not, so without this a captioned
// image in a disappearing-messages chat is stored as if it were empty.
func Unwrap(msg *waE2E.Message) *waE2E.Message {
	// Envelopes do nest (ephemeral wrapping view-once), but the chain is short.
	// The bound is only there so a malformed payload cannot loop forever.
	for i := 0; i < 4 && msg != nil; i++ {
		switch {
		case msg.GetEphemeralMessage().GetMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage().GetMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDocumentWithCaptionMessage().GetMessage() != nil:
			msg = msg.GetDocumentWithCaptionMessage().GetMessage()
		case msg.GetDeviceSentMessage().GetMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		default:
			return msg
		}
	}
	return msg
}

// Text returns the text of a message.
//
// A caption is text, not decoration: when someone sends a screenshot and types
// what to do with it underneath, the caption IS the message. While only
// Conversation and ExtendedTextMessage were read, such a message was stored
// with an empty content column — indistinguishable from an image sent with no
// words at all. Audio messages have no caption field in the protocol.
func Text(msg *waE2E.Message) string {
	msg = Unwrap(msg)
	if msg == nil {
		return ""
	}

	// Try to get text content
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}

	// Media captions
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}

	// Structured messages are rendered as a short text so they are not
	// silently dropped: a shared location or contact is often the whole point.
	if loc := msg.GetLocationMessage(); loc != nil {
		return FormatLocation(loc.GetDegreesLatitude(), loc.GetDegreesLongitude(), loc.GetName(), loc.GetAddress())
	}
	if loc := msg.GetLiveLocationMessage(); loc != nil {
		return FormatLocation(loc.GetDegreesLatitude(), loc.GetDegreesLongitude(), "live location", loc.GetCaption())
	}
	if contact := msg.GetContactMessage(); contact != nil {
		return "[contact] " + contact.GetDisplayName()
	}
	if contacts := msg.GetContactsArrayMessage(); contacts != nil {
		names := make([]string, 0, len(contacts.GetContacts()))
		for _, c := range contacts.GetContacts() {
			names = append(names, c.GetDisplayName())
		}
		return "[contacts] " + strings.Join(names, ", ")
	}
	if event := msg.GetEventMessage(); event != nil {
		return EventText(event)
	}
	if poll := PollCreation(msg); poll != nil {
		options := make([]string, 0, len(poll.GetOptions()))
		for _, o := range poll.GetOptions() {
			options = append(options, o.GetOptionName())
		}
		return fmt.Sprintf("[poll] %s: %s", poll.GetName(), strings.Join(options, " / "))
	}

	// Replies to interactive messages (buttons, lists, templates)
	if reply := msg.GetButtonsResponseMessage(); reply != nil {
		return reply.GetSelectedDisplayText()
	}
	if reply := msg.GetListResponseMessage(); reply != nil {
		return reply.GetTitle()
	}
	if reply := msg.GetTemplateButtonReplyMessage(); reply != nil {
		return reply.GetSelectedDisplayText()
	}
	if buttons := msg.GetButtonsMessage(); buttons != nil {
		return buttons.GetContentText()
	}
	if list := msg.GetListMessage(); list != nil {
		return strings.TrimSpace(list.GetTitle() + "\n" + list.GetDescription())
	}
	if interactive := msg.GetInteractiveMessage(); interactive != nil {
		return interactiveText(interactive)
	}
	// Businesses (couriers, banks, one-time codes) send templates; without
	// these the message had no text and was dropped.
	if tmpl := msg.GetTemplateMessage(); tmpl != nil {
		return templateText(tmpl)
	}
	if hsm := msg.GetHighlyStructuredMessage(); hsm != nil {
		return highlyStructuredText(hsm)
	}

	return ""
}

// joinLines joins the non-empty parts with newlines.
func joinLines(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}

// interactiveText renders header, body, footer and the buttons' labels (and
// link targets) of an interactive message.
func interactiveText(m *waE2E.InteractiveMessage) string {
	var buttons []string
	for _, b := range m.GetNativeFlowMessage().GetButtons() {
		if label := nativeFlowLabel(b.GetButtonParamsJSON()); label != "" {
			buttons = append(buttons, "["+label+"]")
		}
	}
	return joinLines(m.GetHeader().GetTitle(), m.GetHeader().GetSubtitle(), m.GetBody().GetText(),
		m.GetFooter().GetText(), strings.Join(buttons, " "))
}

// nativeFlowLabel reads the display text (and URL, for link buttons) out of
// a native flow button's JSON parameters.
func nativeFlowLabel(paramsJSON string) string {
	var params struct {
		DisplayText string `json:"display_text"`
		URL         string `json:"url"`
		CopyCode    string `json:"copy_code"`
	}
	if json.Unmarshal([]byte(paramsJSON), &params) != nil {
		return ""
	}
	label := params.DisplayText
	switch {
	case params.URL != "":
		label = strings.TrimSpace(label + " " + params.URL)
	case params.CopyCode != "":
		label = strings.TrimSpace(label + " " + params.CopyCode)
	}
	return label
}

// templateText renders a business template: its hydrated text and buttons,
// else its four-row content, else its interactive form.
func templateText(t *waE2E.TemplateMessage) string {
	for _, h := range []*waE2E.TemplateMessage_HydratedFourRowTemplate{t.GetHydratedTemplate(), t.GetHydratedFourRowTemplate()} {
		if h == nil {
			continue
		}
		var buttons []string
		for _, b := range h.GetHydratedButtons() {
			switch {
			case b.GetUrlButton() != nil:
				buttons = append(buttons, "["+strings.TrimSpace(b.GetUrlButton().GetDisplayText()+" "+b.GetUrlButton().GetURL())+"]")
			case b.GetCallButton() != nil:
				buttons = append(buttons, "["+strings.TrimSpace(b.GetCallButton().GetDisplayText()+" "+b.GetCallButton().GetPhoneNumber())+"]")
			case b.GetQuickReplyButton() != nil:
				buttons = append(buttons, "["+b.GetQuickReplyButton().GetDisplayText()+"]")
			}
		}
		if text := joinLines(h.GetHydratedTitleText(), h.GetHydratedContentText(), h.GetHydratedFooterText(), strings.Join(buttons, " ")); text != "" {
			return text
		}
	}
	if f := t.GetFourRowTemplate(); f != nil {
		if text := joinLines(highlyStructuredText(f.GetContent()), highlyStructuredText(f.GetFooter())); text != "" {
			return text
		}
	}
	if i := t.GetInteractiveMessageTemplate(); i != nil {
		return interactiveText(i)
	}
	return ""
}

// highlyStructuredText is the fallback text of a server-side template, or
// its rendered form when the message carries one.
func highlyStructuredText(h *waE2E.HighlyStructuredMessage) string {
	if h == nil {
		return ""
	}
	if hydrated := h.GetHydratedHsm(); hydrated != nil {
		if text := templateText(hydrated); text != "" {
			return text
		}
	}
	return strings.Join(h.GetParams(), " ")
}

// FormatLocation renders a shared location as "[location] lat,lng name address".
func FormatLocation(lat, lng float64, name, address string) string {
	text := fmt.Sprintf("[location] %.6f,%.6f", lat, lng)
	if details := strings.TrimSpace(strings.Join([]string{name, address}, " ")); details != "" {
		text += " " + details
	}
	return text
}

// PollCreation returns the poll a message creates, whichever of the three
// protocol versions carries it, or nil.
func PollCreation(msg *waE2E.Message) *waE2E.PollCreationMessage {
	switch {
	case msg.GetPollCreationMessage() != nil:
		return msg.GetPollCreationMessage()
	case msg.GetPollCreationMessageV2() != nil:
		return msg.GetPollCreationMessageV2()
	case msg.GetPollCreationMessageV3() != nil:
		return msg.GetPollCreationMessageV3()
	}
	return nil
}

// ContextInfo returns the reply and mention metadata of a message.
func ContextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	msg = Unwrap(msg)
	if msg == nil {
		return nil
	}
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage().GetContextInfo()
	case msg.GetLocationMessage() != nil:
		return msg.GetLocationMessage().GetContextInfo()
	case msg.GetEventMessage() != nil:
		return msg.GetEventMessage().GetContextInfo()
	case msg.GetContactMessage() != nil:
		return msg.GetContactMessage().GetContextInfo()
	case msg.GetButtonsResponseMessage() != nil:
		return msg.GetButtonsResponseMessage().GetContextInfo()
	case msg.GetListResponseMessage() != nil:
		return msg.GetListResponseMessage().GetContextInfo()
	}
	return nil
}

// QuotedPreview describes a quoted message for the quoted_text column. Media
// without a caption is shown by its type so the reply still has context.
func QuotedPreview(quoted *waE2E.Message) string {
	if text := Text(quoted); text != "" {
		return text
	}
	if media := Media(quoted, time.Time{}); media.Type != "" {
		return "[" + media.Type + "]"
	}
	return ""
}
