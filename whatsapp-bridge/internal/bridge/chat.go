package bridge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
)

// lastMessageKey is the key of a chat's newest stored message, which
// archive and delete patches carry so other devices apply them to the same
// range of messages.
func (b *Bridge) lastMessageKey(ctx context.Context, chat types.JID) (time.Time, *waCommon.MessageKey) {
	ref, err := b.store.NewestRef(ctx, b.storedChatString(ctx, chat))
	if err != nil {
		return time.Time{}, nil
	}
	key := &waCommon.MessageKey{
		RemoteJID: new(chat.String()),
		FromMe:    new(ref.IsFromMe),
		ID:        new(ref.ID),
	}
	if chat.Server == types.GroupServer && !ref.IsFromMe && ref.SenderJID != "" {
		key.Participant = new(ref.SenderJID)
	}
	return ref.Timestamp, key
}

// ChatAction archives, pins, mutes or deletes a chat on every device of the
// account, through an app state patch, and records the new state locally.
func (b *Bridge) ChatAction(ctx context.Context, req ChatActionRequest) SendMessageResponse {
	chat, err := jid.ParseRecipient(req.ChatJID)
	if err != nil {
		return failed("Invalid chat: %v", err)
	}
	chat = chat.ToNonAD()
	action := strings.ToLower(strings.TrimSpace(req.Action))
	var patch appstate.PatchInfo
	var apply func(stored string) error
	var done string
	switch action {
	case "archive", "unarchive":
		archive := action == "archive"
		ts, key := b.lastMessageKey(ctx, chat)
		patch = appstate.BuildArchive(chat, archive, ts, key)
		apply = func(stored string) error {
			if archive {
				// Archiving also unpins.
				if err := b.store.SetChatFlag(ctx, stored, "pinned", false); err != nil {
					return err
				}
			}
			return b.store.SetChatFlag(ctx, stored, "archived", archive)
		}
		done = action + "d"
	case "pin", "unpin":
		pin := action == "pin"
		patch = appstate.BuildPin(chat, pin)
		apply = func(stored string) error { return b.store.SetChatFlag(ctx, stored, "pinned", pin) }
		done = action + "ned"
	case "mute":
		if req.MuteHours < 0 {
			return failed("mute_hours must not be negative (0 mutes forever)")
		}
		duration := time.Duration(req.MuteHours * float64(time.Hour))
		patch = appstate.BuildMute(chat, true, duration)
		until := int64(-1)
		if duration > 0 {
			until = time.Now().Add(duration).UnixMilli()
		}
		apply = func(stored string) error { return b.store.SetMutedUntil(ctx, stored, until) }
		done = "muted"
		if duration > 0 {
			done += fmt.Sprintf(" for %s", duration.Round(time.Minute))
		} else {
			done += " forever"
		}
	case "unmute":
		patch = appstate.BuildMute(chat, false, 0)
		apply = func(stored string) error { return b.store.SetMutedUntil(ctx, stored, 0) }
		done = "unmuted"
	case "delete":
		ts, key := b.lastMessageKey(ctx, chat)
		patch = appstate.BuildDeleteChat(chat, ts, key, true)
		apply = func(stored string) error { return b.store.MarkChatDeleted(ctx, stored, time.Now()) }
		done = "deleted (hidden on every device; the bridge keeps its copy of the messages)"
	default:
		return failed("unknown action %q (want archive, unarchive, pin, unpin, mute, unmute or delete)", req.Action)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if err := b.client.SendAppState(ctx, patch); err != nil {
		return failed("Failed to %s %s: %v", action, chat, err)
	}
	if err := apply(b.storedChatString(ctx, chat)); err != nil {
		b.log.Warn("failed to record chat action", "action", action, "chat", chat.String(), "err", err)
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Chat %s %s", chat, done)}
}

// resolveLabel accepts a label id or a (case-insensitive) label name.
func (b *Bridge) resolveLabel(ctx context.Context, label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", errors.New("label is required")
	}
	id, err := b.store.FindLabel(ctx, label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no label %q (list_labels shows the existing ones; labels exist on WhatsApp Business accounts)", label)
	}
	return id, err
}

// LabelChat adds a label to a chat or removes it, on every device.
func (b *Bridge) LabelChat(ctx context.Context, req LabelChatRequest) SendMessageResponse {
	chat, err := jid.ParseRecipient(req.ChatJID)
	if err != nil {
		return failed("Invalid chat: %v", err)
	}
	chat = chat.ToNonAD()
	labelID, err := b.resolveLabel(ctx, req.Label)
	if err != nil {
		return failed("%v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if err := b.client.SendAppState(ctx, appstate.BuildLabelChat(chat, labelID, req.Labeled)); err != nil {
		return failed("Failed to change label: %v", err)
	}
	if err := b.store.SetChatLabel(ctx, b.storedChatString(ctx, chat), labelID, req.Labeled); err != nil {
		b.log.Warn("failed to record chat label", "chat", chat.String(), "err", err)
	}
	verb := "added to"
	if !req.Labeled {
		verb = "removed from"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Label %s %s %s", labelID, verb, chat)}
}

// EditLabel creates, renames, recolours or deletes a label.
func (b *Bridge) EditLabel(ctx context.Context, req LabelEditRequest) SendMessageResponse {
	name := strings.TrimSpace(req.Name)
	if name == "" && !req.Deleted {
		return failed("name is required")
	}
	if req.Color < 0 || req.Color > 19 {
		return failed("color must be 0 to 19 (WhatsApp's label palette)")
	}
	id := strings.TrimSpace(req.LabelID)
	if id == "" {
		if req.Deleted {
			return failed("label_id is required to delete a label")
		}
		next, err := b.store.NextLabelID(ctx)
		if err != nil {
			return failed("Failed to pick a label id: %v", err)
		}
		id = next
	} else if existing, err := b.store.Label(ctx, id); err == nil && name == "" {
		name = existing
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if err := b.client.SendAppState(ctx, appstate.BuildLabelEdit(id, name, req.Color, req.Deleted)); err != nil {
		return failed("Failed to edit label: %v", err)
	}
	if err := b.store.SaveLabel(ctx, id, name, req.Color, req.Deleted); err != nil {
		b.log.Warn("failed to record label", "label", id, "err", err)
	}
	verb := "saved"
	if req.Deleted {
		verb = "deleted"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Label %s %q %s", id, name, verb), MessageID: id}
}
