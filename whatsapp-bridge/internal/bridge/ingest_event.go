package bridge

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// decryptEventResponse decrypts an "I'm going" answer. whatsmeow has no
// exported helper for event responses; this follows its message-secret
// scheme (msgsecret.go): HKDF over the event's secret, AES-GCM with the event
// id and responder as additional data.
func (b *Bridge) decryptEventResponse(ctx context.Context, evt *events.Message) (*waE2E.EventResponseMessage, error) {
	enc := evt.Message.GetEncEventResponseMessage()
	key := enc.GetEventCreationMessageKey()
	origSender, err := keyAuthor(evt.Info, key)
	if err != nil {
		return nil, err
	}
	secret, storedSender, err := b.client.MessageSecret(ctx, evt.Info.Chat, origSender, key.GetID())
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, fmt.Errorf("secret of event %s unknown (sent before the bridge was linked)", key.GetID())
	}
	var plaintext []byte
	for _, sender := range []types.JID{origSender, storedSender} {
		if sender.IsEmpty() {
			continue
		}
		k, ad := eventResponseKey(evt.Info.Sender, key.GetID(), sender, secret)
		if plaintext, err = gcmutil.Decrypt(k, enc.GetEncIV(), enc.GetEncPayload(), ad); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	var resp waE2E.EventResponseMessage
	if err := proto.Unmarshal(plaintext, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func eventResponseKey(responder types.JID, eventID string, eventSender types.JID, secret []byte) ([]byte, []byte) {
	responderStr := responder.ToNonAD().String()
	useCase := eventID + eventSender.ToNonAD().String() + responderStr + "Event Response"
	return hkdfutil.SHA256(secret, nil, []byte(useCase), 32), fmt.Appendf(nil, "%s\x00%s", eventID, responderStr)
}

// keyAuthor is the author of the message a key points at, seen from a
// message (vote, response) that refers to it.
func keyAuthor(info types.MessageInfo, key *waCommon.MessageKey) (types.JID, error) {
	switch {
	case key.GetFromMe():
		return info.Sender, nil
	case info.Chat.Server == types.DefaultUserServer || info.Chat.Server == types.HiddenUserServer:
		return types.ParseJID(key.GetRemoteJID())
	default:
		return types.ParseJID(key.GetParticipant())
	}
}

func (b *Bridge) onEventResponse(ctx context.Context, evt *events.Message, live bool) {
	eventID := evt.Message.GetEncEventResponseMessage().GetEventCreationMessageKey().GetID()
	resp, err := b.decryptEventResponse(ctx, evt)
	if err != nil {
		b.log.Warn("failed to decrypt event response", "event_id", eventID, "err", err)
		return
	}
	ts := evt.Info.Timestamp
	if ms := resp.GetTimestampMS(); ms > 0 {
		ts = time.UnixMilli(ms)
	}
	b.storeEventResponse(ctx, evt.Info.Chat, eventID, evt.Info.Sender, evt.Info.SenderAlt, evt.Info.PushName, resp, ts, live)
}

// storeHistoryEventResponses stores the answers history sync delivers with
// the event, already decrypted.
func (b *Bridge) storeHistoryEventResponses(ctx context.Context, chat types.JID, eventID string, web *waWeb.WebMessageInfo) {
	for _, r := range web.GetEventResponses() {
		responder := b.keySender(chat, r.GetEventResponseMessageKey())
		b.storeEventResponse(ctx, chat, eventID, responder, types.EmptyJID, "", r.GetEventResponseMessage(),
			time.UnixMilli(r.GetTimestampMS()), false)
	}
}

func (b *Bridge) storeEventResponse(ctx context.Context, chat types.JID, eventID string, responder, alt types.JID,
	pushName string, resp *waE2E.EventResponseMessage, ts time.Time, live bool) {
	answer, ok := content.EventResponseName(resp.GetResponse())
	if !ok || eventID == "" {
		return
	}
	r := store.EventResponse{
		MessageID: eventID, ChatJID: b.chatOf(ctx, chat, eventID), ResponderJID: responder.ToNonAD().String(),
		ResponderName: b.resolveName(ctx, responder, alt, pushName), Response: answer,
		ExtraGuests: int(resp.GetExtraGuestCount()), Timestamp: ts,
	}
	if err := b.store.SaveEventResponse(ctx, r); err != nil {
		b.log.Warn("failed to store event response", "err", err)
		return
	}
	if live {
		b.events.Publish(hub.Event{
			Type: "event_response", ChatJID: r.ChatJID, MessageID: eventID, SenderJID: r.ResponderJID,
			SenderName: r.ResponderName, Content: answer, Timestamp: ts,
		})
	}
}
