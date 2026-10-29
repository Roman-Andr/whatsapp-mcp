package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// groupNotice is one line of group history: someone joined, left, was made
// admin, the name changed.
type groupNotice struct {
	kind string    // join, leave, promote, demote, name, description, settings
	user types.JID // whom it is about; the actor for name and settings changes
	text string
}

// onGroupInfo records membership and settings changes as messages of the
// group, publishes them as "group" events and welcomes newcomers.
func (b *Bridge) onGroupInfo(ctx context.Context, evt *events.GroupInfo) {
	var actor types.JID
	if evt.Sender != nil {
		actor = *evt.Sender
	}
	actorPN := types.EmptyJID
	if evt.SenderPN != nil {
		actorPN = *evt.SenderPN
	}
	actorName := ""
	if !actor.IsEmpty() {
		actorName = b.memberName(ctx, actor, actorPN, "")
	}
	by := func(user types.JID) string {
		if actorName == "" || actor.User == user.User {
			return ""
		}
		return " (by " + actorName + ")"
	}
	var notices []groupNotice
	for _, u := range evt.Join {
		verb := "joined"
		if evt.JoinReason == "invite" {
			verb = "joined with the invite link"
		} else if !actor.IsEmpty() && actor.User != u.User {
			verb = "was added"
		}
		notices = append(notices, groupNotice{kind: "join", user: u, text: b.memberName(ctx, u, types.EmptyJID, "") + " " + verb + by(u)})
	}
	for _, u := range evt.Leave {
		verb := "left"
		if !actor.IsEmpty() && actor.User != u.User {
			verb = "was removed"
		}
		notices = append(notices, groupNotice{kind: "leave", user: u, text: b.memberName(ctx, u, types.EmptyJID, "") + " " + verb + by(u)})
	}
	for _, u := range evt.Promote {
		notices = append(notices, groupNotice{kind: "promote", user: u, text: b.memberName(ctx, u, types.EmptyJID, "") + " is now an admin" + by(u)})
	}
	for _, u := range evt.Demote {
		notices = append(notices, groupNotice{kind: "demote", user: u, text: b.memberName(ctx, u, types.EmptyJID, "") + " is no longer an admin" + by(u)})
	}
	if evt.Name != nil {
		notices = append(notices, groupNotice{kind: "name", user: actor, text: fmt.Sprintf("name changed to %q%s", evt.Name.Name, by(types.EmptyJID))})
	}
	if evt.Topic != nil {
		notices = append(notices, groupNotice{kind: "description", user: actor, text: "description changed" + by(types.EmptyJID) + ": " + evt.Topic.Topic})
	}
	if evt.Announce != nil {
		state := "everyone may send"
		if evt.Announce.IsAnnounce {
			state = "only admins may send"
		}
		notices = append(notices, groupNotice{kind: "settings", user: actor, text: state + by(types.EmptyJID)})
	}
	if len(notices) == 0 {
		return
	}

	groupJID := evt.JID.String()
	name := b.store.ChatName(ctx, groupJID)
	if evt.Name != nil {
		name = evt.Name.Name
	}
	at := evt.Timestamp
	if at.IsZero() {
		at = time.Now()
	}
	if err := b.store.SaveChat(ctx, groupJID, name, at); err != nil {
		b.log.Warn("failed to store chat", "err", err)
	}
	for _, n := range notices {
		// The id is derived from the change so a repeated notification
		// does not add a second line.
		id := fmt.Sprintf("group-%s-%d-%s", n.kind, at.Unix(), n.user.User)
		if err := b.store.SaveMessage(ctx, store.Message{
			ID: id, ChatJID: groupJID, Sender: actor.User, SenderJID: jid.String(actor), SenderName: actorName,
			Content: "[group] " + n.text, Timestamp: at,
		}); err != nil {
			b.log.Warn("failed to store group notice", "err", err)
			continue
		}
		b.log.Info("group notice", "at", at.Format(time.DateTime), "group", name, "text", n.text)
		b.events.Publish(hub.Event{
			Type: "group", ChatJID: groupJID, ChatName: name, MessageID: id, SenderJID: jid.String(n.user),
			Content: n.kind + ": " + n.text, Timestamp: at,
		})
	}
	for _, u := range evt.Join {
		b.welcome(ctx, evt.JID, u)
	}
}

// welcome sends the configured welcome message for group to a newcomer.
// Members cannot see what was posted before they joined, so a short note
// with what the group is about keeps them from leaving an empty-looking
// chat. Read-only mode, the send allowlist and the rate limit apply as to
// any other send, and it is audited.
func (b *Bridge) welcome(ctx context.Context, group, user types.JID) {
	template, ok := b.welcomes[group.String()]
	if !ok || b.client == nil {
		return
	}
	if own := b.ownJID(user); !own.IsEmpty() && own.User == user.User {
		return
	}
	entry := audit.Entry{Endpoint: "welcome", Chat: group.String(), Text: template}
	if b.readOnly {
		entry.Result = "skipped: read-only mode"
		b.audit.Record(entry)
		return
	}
	if ok, _ := b.limiter.Take(); !ok {
		entry.Result = "skipped: send rate limit reached"
		b.audit.Record(entry)
		return
	}
	text := strings.ReplaceAll(template, "{name}", b.memberName(ctx, user, types.EmptyJID, ""))
	req := SendMessageRequest{Recipient: group.String(), Message: text}
	if strings.Contains(text, "{mention}") {
		req.Message = strings.ReplaceAll(text, "{mention}", "@"+user.User)
		req.Mentions = []string{user.ToNonAD().String()}
	}
	entry.Text = req.Message
	resp := b.Send(ctx, req)
	entry.Success, entry.Result, entry.MessageID = resp.Success, resp.Message, resp.MessageID
	b.audit.Record(entry)
	if !resp.Success {
		b.log.Warn("failed to welcome a new member", "user", user.String(), "group", group.String(), "err", resp.Message)
	}
}

// onJoinedGroup records a group this account was added to.
func (b *Bridge) onJoinedGroup(ctx context.Context, evt *events.JoinedGroup) {
	groupJID := evt.JID.String()
	if err := b.store.SaveChat(ctx, groupJID, evt.Name, time.Now()); err != nil {
		b.log.Warn("failed to store joined group", "err", err)
		return
	}
	text := "you were added to the group"
	if evt.Reason == "invite" {
		text = "you joined with an invite link"
	} else if evt.Type == "new" {
		text = "the group was created"
	}
	b.events.Publish(hub.Event{Type: "group", ChatJID: groupJID, ChatName: evt.Name, Content: "joined: " + text, Timestamp: time.Now()})
}
