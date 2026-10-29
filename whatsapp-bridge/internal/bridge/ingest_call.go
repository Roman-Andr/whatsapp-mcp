package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// callLog follows calls from offer to end, so each call becomes one line in
// its chat: "[call] missed voice call", "[call] video call, answered".
type callLog struct {
	mu    sync.Mutex
	calls map[string]*callRecord
}

type callRecord struct {
	chat     types.JID
	caller   types.JID
	media    string // voice or video
	group    bool
	at       time.Time
	answered bool
}

func newCallLog() *callLog { return &callLog{calls: map[string]*callRecord{}} }

// callMedia reads whether an offer is a video call.
func callMedia(evt *events.CallOffer) string {
	if evt.Data != nil && evt.Data.GetChildByTag("video").Tag == "video" {
		return "video"
	}
	return "voice"
}

func (r *callRecord) text(state string) string {
	kind := r.media + " call"
	if r.group {
		kind = "group " + kind
	}
	switch state {
	case "ringing":
		return "[call] incoming " + kind
	case "answered":
		return "[call] " + kind + ", answered"
	case "missed":
		return "[call] missed " + kind
	case "rejected":
		return "[call] " + kind + ", rejected automatically"
	}
	return fmt.Sprintf("[call] %s, ended (%s)", kind, state)
}

// track follows a call event and returns the call it belongs to with its
// new state; state is empty for an event that changes nothing to record.
func (l *callLog) track(evt any) (meta types.BasicCallMeta, rec *callRecord, state string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch v := evt.(type) {
	case *events.CallOffer:
		meta = v.BasicCallMeta
		rec = &callRecord{media: callMedia(v)}
		state = "ringing"
	case *events.CallOfferNotice:
		meta = v.BasicCallMeta
		media := "voice"
		if v.Media == "video" {
			media = "video"
		}
		rec = &callRecord{media: media, group: v.Type == "group"}
		state = "ringing"
	case *events.CallAccept:
		meta = v.BasicCallMeta
		if rec = l.calls[meta.CallID]; rec != nil {
			rec.answered = true
			state = "answered"
		}
	case *events.CallTerminate:
		meta = v.BasicCallMeta
		if rec = l.calls[meta.CallID]; rec != nil {
			delete(l.calls, meta.CallID)
			state = "missed"
			if rec.answered {
				state = v.Reason
				if state == "" {
					state = "hung up"
				}
			}
		}
	}
	if rec != nil && state == "ringing" {
		caller := meta.CallCreator
		if caller.IsEmpty() {
			caller = meta.From
		}
		rec.caller = caller.ToNonAD()
		rec.chat = rec.caller
		if !meta.GroupJID.IsEmpty() {
			rec.chat = meta.GroupJID
			rec.group = true
		}
		rec.at = meta.Timestamp
		if rec.at.IsZero() {
			rec.at = time.Now()
		}
		l.calls[meta.CallID] = rec
	}
	return meta, rec, state
}

// onCall stores calls as messages of the caller's (or group's) chat and
// publishes "call" events, so an agent watching a chat sees that someone
// tried to call.
func (b *Bridge) onCall(ctx context.Context, evt any) {
	if b.calls == nil {
		return
	}
	meta, rec, state := b.calls.track(evt)
	if rec == nil || state == "" {
		return
	}
	offer, isOffer := evt.(*events.CallOffer)
	rejected := isOffer && b.rejectCall(ctx, offer, rec)
	if rejected {
		state = "rejected"
		b.calls.mu.Lock()
		delete(b.calls.calls, meta.CallID)
		b.calls.mu.Unlock()
	}

	chat := b.storedChat(ctx, rec.chat)
	chatJID := chat.String()
	name := b.store.ChatName(ctx, chatJID)
	if name == "" && b.client != nil {
		name = b.chatName(ctx, chat, nil, rec.caller.User)
	}
	if err := b.store.SaveChat(ctx, chatJID, name, rec.at); err != nil {
		b.log.Warn("failed to store chat", "err", err)
	}
	callerName := b.resolveName(ctx, rec.caller, meta.CallCreatorAlt, "")
	text := rec.text(state)
	id := "call-" + meta.CallID
	if err := b.store.SaveMessage(ctx, store.Message{
		ID: id, ChatJID: chatJID, Sender: rec.caller.User, SenderJID: rec.caller.String(), SenderName: callerName,
		Content: text, Timestamp: rec.at,
	}); err != nil {
		b.log.Warn("failed to store call", "call_id", meta.CallID, "err", err)
		return
	}
	b.log.Info("call", "at", rec.at.Format(time.DateTime), "caller", firstNonEmpty(callerName, rec.caller.User), "text", text)
	b.events.Publish(hub.Event{
		Type: "call", ChatJID: chatJID, ChatName: name, MessageID: id, SenderJID: rec.caller.String(),
		SenderName: callerName, Content: text, State: state, Timestamp: time.Now(),
	})
}

// callRejecter is WHATSAPP_REJECT_CALLS: decline incoming 1:1 calls, and
// with WHATSAPP_REJECT_CALLS_MESSAGE tell the caller why.
type callRejecter struct {
	message string
}

// newCallRejecter returns nil unless calls are to be rejected.
func newCallRejecter(c CallRejection) *callRejecter {
	if !c.Enabled {
		return nil
	}
	return &callRejecter{message: c.Message}
}

// rejectCall declines an incoming call when configured, and reports whether
// it did. The optional reply is a send like any other: read-only mode, the
// send allowlist and the rate limit apply, and it is audited.
func (b *Bridge) rejectCall(ctx context.Context, offer *events.CallOffer, rec *callRecord) bool {
	if b.rejectCalls == nil || rec.group || b.client == nil {
		return false
	}
	if err := b.client.RejectCall(ctx, offer.From, offer.CallID); err != nil {
		b.log.Warn("failed to reject call", "call_id", offer.CallID, "err", err)
		return false
	}
	text := b.rejectCalls.message
	if text == "" {
		return true
	}
	entry := audit.Entry{Endpoint: "call_reply", Chat: rec.caller.String(), Text: text}
	switch ok, _ := b.limiter.Take(); {
	case b.readOnly:
		entry.Result = "skipped: read-only mode"
	case !ok:
		entry.Result = "skipped: send rate limit reached"
	default:
		resp := b.Send(ctx, SendMessageRequest{Recipient: rec.caller.String(), Message: text})
		entry.Success, entry.Result, entry.MessageID = resp.Success, resp.Message, resp.MessageID
	}
	b.audit.Record(entry)
	return true
}
