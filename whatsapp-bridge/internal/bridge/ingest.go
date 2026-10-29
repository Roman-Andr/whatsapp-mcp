package bridge

import (
	"fmt"

	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// HandleEvent dispatches whatsmeow events. whatsmeow calls it synchronously,
// one event at a time, so it adds no timeout of its own: the context is the
// application's, cancelled at shutdown.
func (b *Bridge) HandleEvent(evt any) {
	ctx := b.base
	switch v := evt.(type) {
	case *events.Message:
		b.processMessage(ctx, v, true)
	case *events.UndecryptableMessage:
		b.onUndecryptable(ctx, v)
	case *events.HistorySync:
		b.onHistorySync(ctx, v)
	case *events.Receipt:
		b.onReceipt(ctx, v)
	case *events.MarkChatAsRead:
		b.onMarkChatAsRead(ctx, v)
	case *events.GroupInfo:
		b.onGroupInfo(ctx, v)
	case *events.JoinedGroup:
		b.onJoinedGroup(ctx, v)
	case *events.Archive, *events.Pin, *events.Mute, *events.DeleteChat, *events.LabelEdit, *events.LabelAssociationChat:
		b.onChatState(ctx, v)
	case *events.CallOffer, *events.CallOfferNotice, *events.CallAccept, *events.CallTerminate:
		b.onCall(ctx, v)
	case *events.Star:
		b.onStar(ctx, v)
	case *events.Presence:
		b.onPresence(ctx, v)
	case *events.MediaRetry:
		if b.media != nil {
			b.media.HandleRetry(v)
		}
	case *events.Connected:
		b.SetSession(wa.SessionConnected, "", false)
		b.MergeLIDChats(ctx)
		// LID mappings learned since the start widen the published list.
		if err := b.PublishReadAllowlist(ctx); err != nil {
			b.log.Warn("failed to publish read allowlist", "err", err)
		}
	case *events.Disconnected:
		b.SetSession(wa.SessionDisconnected, "connection lost, reconnecting", false)
	case *events.LoggedOut:
		b.SetSession(wa.SessionLoggedOut, wa.LoggedOutReason(v.Reason, v.OnConnect), true)
	case *events.StreamReplaced:
		b.SetSession(wa.SessionStreamReplaced, wa.ReasonStreamReplaced, true)
	case *events.QR:
		b.SetSession(wa.SessionQRRequired, wa.ReasonQRRequired, true)
	case *events.TemporaryBan:
		b.SetSession(wa.SessionTemporaryBan, v.String(), true)
	case *events.ClientOutdated:
		b.SetSession(wa.SessionClientOutdated, wa.ReasonClientOutdated, true)
	case *events.ConnectFailure:
		b.SetSession(wa.SessionConnectFailure, fmt.Sprintf("%s %s", v.Reason, v.Message), true)
	}
}
