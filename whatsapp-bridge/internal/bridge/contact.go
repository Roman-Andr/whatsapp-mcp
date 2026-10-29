package bridge

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
)

// presenceSettle is how long Presence waits after subscribing: the first
// update usually follows within a second.
const presenceSettle = 1500 * time.Millisecond

// presenceLog keeps the last presence seen for each subscribed user.
type presenceLog struct {
	mu   sync.Mutex
	last map[string]PresenceInfo
	// done holds the one-time steps that finished: going online (key "")
	// and subscribing to a user (key: the stored JID).
	done map[string]bool
	// running holds the steps in flight; the channel is closed when the step
	// ends, so a concurrent caller waits instead of repeating it.
	running map[string]chan struct{}
}

func newPresenceLog() *presenceLog {
	return &presenceLog{last: map[string]PresenceInfo{}, done: map[string]bool{}, running: map[string]chan struct{}{}}
}

// once runs step for key unless it already succeeded. At most one step per
// key is in flight: other callers wait for it and, if it failed, try again
// themselves. ran reports whether this call ran or waited for a step that
// was not done before.
func (p *presenceLog) once(ctx context.Context, key string, step func() error) (ran bool, err error) {
	for {
		p.mu.Lock()
		if p.done[key] {
			p.mu.Unlock()
			return ran, nil
		}
		if wait, busy := p.running[key]; busy {
			p.mu.Unlock()
			select {
			case <-wait:
				ran = true
				continue
			case <-ctx.Done():
				return ran, ctx.Err()
			}
		}
		finished := make(chan struct{})
		p.running[key] = finished
		p.mu.Unlock()

		err := step()
		p.mu.Lock()
		delete(p.running, key)
		if err == nil {
			p.done[key] = true
		}
		p.mu.Unlock()
		close(finished)
		return true, err
	}
}

func (b *Bridge) onPresence(ctx context.Context, evt *events.Presence) {
	if b.presence == nil {
		return
	}
	jid := b.storedChat(ctx, evt.From).String()
	info := PresenceInfo{JID: jid, Online: !evt.Unavailable, LastSeen: evt.LastSeen, Updated: time.Now(), Known: true}
	b.presence.mu.Lock()
	b.presence.last[jid] = info
	b.presence.mu.Unlock()
	state := "offline"
	if info.Online {
		state = "online"
	}
	b.events.Publish(hub.Event{Type: "presence", ChatJID: jid, State: state, Timestamp: info.Updated})
}

// Presence subscribes to a user's presence (once) and reports the latest
// known state. WhatsApp only sends presence to clients that are themselves
// Online, and an online companion stops notifications on the phone; that is
// why WHATSAPP_PRESENCE has to allow it.
func (b *Bridge) Presence(ctx context.Context, jidStr string) (PresenceInfo, error) {
	if !b.presenceEnabled {
		return PresenceInfo{}, fmt.Errorf("presence is off: set WHATSAPP_PRESENCE=1 on the bridge (the bridge then shows as online, which stops notifications on your phone while it runs)")
	}
	jid, err := jid.ParseRecipient(jidStr)
	if err != nil {
		return PresenceInfo{}, err
	}
	if jid.Server == types.GroupServer {
		return PresenceInfo{}, fmt.Errorf("presence is per person, not per group")
	}
	if err := b.checkReadable(ctx, jid); err != nil {
		return PresenceInfo{}, err
	}
	if !b.Online() {
		return PresenceInfo{}, fmt.Errorf("not connected to WhatsApp")
	}
	key := b.storedChat(ctx, jid).String()
	if _, err := b.presence.once(ctx, "", func() error {
		return b.client.SendPresence(ctx, types.PresenceAvailable)
	}); err != nil {
		return PresenceInfo{}, fmt.Errorf("failed to go online: %w", err)
	}
	subscribed, err := b.presence.once(ctx, key, func() error {
		return b.client.SubscribePresence(ctx, jid)
	})
	if err != nil {
		return PresenceInfo{}, fmt.Errorf("failed to subscribe: %w", err)
	}
	if subscribed {
		select {
		case <-time.After(presenceSettle):
		case <-ctx.Done():
		}
	}
	b.presence.mu.Lock()
	defer b.presence.mu.Unlock()
	info, ok := b.presence.last[key]
	if !ok {
		return PresenceInfo{JID: key}, nil
	}
	return info, nil
}

// ContactInfo looks a person (or a group's picture) up.
func (b *Bridge) ContactInfo(ctx context.Context, req ContactInfoRequest) ContactInfo {
	jid, err := jid.ParseRecipient(req.JID)
	if err != nil {
		return ContactInfo{Message: fmt.Sprintf("Invalid JID: %v", err)}
	}
	jid = jid.ToNonAD()
	if err := b.checkReadable(ctx, jid); err != nil {
		return ContactInfo{Message: err.Error()}
	}
	if !b.Online() {
		return ContactInfo{Message: "Not connected to WhatsApp"}
	}
	out := ContactInfo{Success: true, JID: jid.String()}
	if jid.Server == types.GroupServer {
		out.Name = b.store.ChatName(ctx, jid.String())
	} else {
		out.Name = b.memberName(ctx, jid, types.EmptyJID, "")
		if jid.Server == types.DefaultUserServer {
			if res, err := b.client.IsOnWhatsApp(ctx, []string{"+" + jid.User}); err == nil && len(res) == 1 {
				out.OnWhatsApp = &res[0].IsIn
			}
		}
		if infos, err := b.client.GetUserInfo(ctx, []types.JID{jid}); err == nil {
			for _, info := range infos {
				out.About = info.Status
				if info.VerifiedName != nil && info.VerifiedName.Details != nil {
					out.BusinessName = info.VerifiedName.Details.GetVerifiedName()
				}
			}
		}
		if list, err := b.client.GetBlocklist(ctx, ""); err == nil {
			for _, alias := range b.jidAliases(ctx, jid) {
				if list.IsBlocked(alias) {
					out.Blocked = true
				}
			}
		}
	}
	if req.Picture {
		if path, err := b.media.Avatar(ctx, jid); err != nil {
			out.PictureError = err.Error()
		} else {
			out.PicturePath = path
		}
	}
	return out
}

// SetBlocked blocks or unblocks a person.
func (b *Bridge) SetBlocked(ctx context.Context, req BlockRequest) SendMessageResponse {
	jid, err := jid.ParseRecipient(req.JID)
	if err != nil {
		return failed("Invalid JID: %v", err)
	}
	if jid.Server == types.GroupServer {
		return failed("groups cannot be blocked; leave the group instead")
	}
	if err := b.checkAllowed(ctx, jid); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	action, verb := events.BlocklistChangeActionBlock, "Blocked"
	if !req.Blocked {
		action, verb = events.BlocklistChangeActionUnblock, "Unblocked"
	}
	if _, err := b.client.UpdateBlocklist(ctx, jid.ToNonAD(), action, ""); err != nil {
		return failed("Failed to change the blocklist: %v", err)
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("%s %s", verb, jid.ToNonAD())}
}

// Blocklist lists the blocked people the read allowlist shows, with the
// HTTP status to answer.
func (b *Bridge) Blocklist(ctx context.Context) (map[string]any, int) {
	if !b.Online() {
		return map[string]any{"success": false, "message": "Not connected to WhatsApp"}, http.StatusInternalServerError
	}
	list, err := b.client.GetBlocklist(ctx, "")
	if err != nil {
		return map[string]any{"success": false, "message": fmt.Sprintf("Failed to get the blocklist: %v", err)}, http.StatusInternalServerError
	}
	type entry struct {
		JID  string `json:"jid"`
		Name string `json:"name,omitempty"`
	}
	out := []entry{}
	for _, item := range list.Items {
		jid := item.PN
		if jid.IsEmpty() {
			jid = item.LID
		}
		if jid.IsEmpty() || b.checkReadable(ctx, jid) != nil {
			continue
		}
		out = append(out, entry{JID: jid.String(), Name: b.memberName(ctx, jid, item.PN, "")})
	}
	return map[string]any{"success": true, "blocked": out}, http.StatusOK
}
