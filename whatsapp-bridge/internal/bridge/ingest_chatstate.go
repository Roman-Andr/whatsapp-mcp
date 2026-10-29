package bridge

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

func (b *Bridge) onChatState(ctx context.Context, evt any) {
	var err error
	switch v := evt.(type) {
	case *events.Archive:
		err = b.store.SetChatFlag(ctx, b.storedChat(ctx, v.JID).String(), "archived", v.Action.GetArchived())
	case *events.Pin:
		err = b.store.SetChatFlag(ctx, b.storedChat(ctx, v.JID).String(), "pinned", v.Action.GetPinned())
	case *events.Mute:
		until := int64(0)
		if v.Action.GetMuted() {
			until = v.Action.GetMuteEndTimestamp()
			if until == 0 {
				until = -1
			}
		}
		err = b.store.SetMutedUntil(ctx, b.storedChat(ctx, v.JID).String(), until)
	case *events.DeleteChat:
		at := v.Timestamp
		if at.IsZero() {
			at = time.Now()
		}
		err = b.store.MarkChatDeleted(ctx, b.storedChat(ctx, v.JID).String(), at)
	case *events.LabelEdit:
		err = b.store.SaveLabel(ctx, v.LabelID, v.Action.GetName(), v.Action.GetColor(), v.Action.GetDeleted())
	case *events.LabelAssociationChat:
		err = b.store.SetChatLabel(ctx, b.storedChat(ctx, v.JID).String(), v.LabelID, v.Action.GetLabeled())
	}
	if err != nil {
		b.log.Warn("failed to record chat state change", "event", fmt.Sprintf("%T", evt), "err", err)
	}
}
