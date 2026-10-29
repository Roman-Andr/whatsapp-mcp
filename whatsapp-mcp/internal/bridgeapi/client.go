package bridgeapi

// The bridge's REST API. Everything that changes WhatsApp state (sending,
// reacting, downloading, requesting history) goes through it; it requires
// the token the bridge keeps in store/api_token.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

const (
	// RequestTimeout bounds a call to the bridge.
	RequestTimeout = 60 * time.Second
	// DownloadTimeout bounds a media download, which may re-request the
	// media from the phone.
	DownloadTimeout = 150 * time.Second
	// StatusTimeout bounds a status check, which must answer quickly to
	// be useful.
	StatusTimeout = 10 * time.Second
	// StatusJID is the chat of status updates (stories).
	StatusJID = "status@broadcast"
)

// Filter is the read allowlist of the event stream: which chats may pass
// (a nil func: every chat), or why that cannot be told.
type Filter func(ctx context.Context) (func(chatJID string) bool, error)

// Client calls one bridge's REST API.
type Client struct {
	// BaseURL is the API's URL, ending in /api.
	BaseURL string
	// Token is the API token, asked for on every request: the bridge's
	// token file may change while the server runs. With an error or ""
	// requests go without one.
	Token func() (string, error)
	// HTTP sends the requests (NewHTTPClient).
	HTTP *http.Client
}

// NewHTTPClient is the keep-alive client for every call: a connection to
// the bridge is reused instead of a new TCP connection per call.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}}
}

// request sends a request within timeout of the caller's ctx; the
// returned cancel func ends it once the body is read.
func (c *Client) request(ctx context.Context, method, endpoint string, body any, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var reader io.Reader
	if body != nil {
		data, err := pyfmt.Marshal(body)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/"+endpoint, reader)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != nil {
		if token, err := c.Token(); err == nil && token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// requestFailed is the answer of a request that got no response.
func requestFailed(err error, timeout time.Duration) *pyfmt.OMap {
	if isTimeout(err) {
		return pyfmt.NewOMap("success", false, "message", fmt.Sprintf("Bridge did not answer within %ds", int(timeout.Seconds())))
	}
	return pyfmt.NewOMap("success", false, "message", fmt.Sprintf("Request error (is the bridge running?): %v", err))
}

// post sends a request to the bridge and returns its JSON answer, never failing.
func (c *Client) post(ctx context.Context, endpoint string, payload *pyfmt.OMap, timeout time.Duration) *pyfmt.OMap {
	resp, cancel, err := c.request(ctx, http.MethodPost, endpoint, payload, timeout)
	if err != nil {
		return requestFailed(err, timeout)
	}
	defer cancel()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return requestFailed(err, timeout)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return pyfmt.NewOMap("success", false, "message", "Bridge rejected the API token (check store/api_token or WHATSAPP_BRIDGE_TOKEN)")
	}
	decoded, err := pyfmt.DecodeOrdered(data)
	result, ok := decoded.(*pyfmt.OMap)
	if err != nil || !ok {
		return pyfmt.NewOMap("success", false, "message", fmt.Sprintf("Error: HTTP %d - %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	if resp.StatusCode != http.StatusOK && result.Get("success") == nil {
		result.Set("success", false)
	}
	return result
}

// get fetches a bridge endpoint's JSON answer.
func (c *Client) get(ctx context.Context, endpoint string, timeout time.Duration) (*pyfmt.OMap, int, string, error) {
	resp, cancel, err := c.request(ctx, http.MethodGet, endpoint, nil, timeout)
	if err != nil {
		return nil, 0, "", err
	}
	defer cancel()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, "", err
	}
	decoded, err := pyfmt.DecodeOrdered(data)
	if err != nil {
		return nil, resp.StatusCode, string(data), err
	}
	result, ok := decoded.(*pyfmt.OMap)
	if !ok {
		return nil, resp.StatusCode, string(data), fmt.Errorf("unexpected answer: %s", strings.TrimSpace(string(data)))
	}
	return result, resp.StatusCode, string(data), nil
}

// getOrError is a GET whose failures come back as {success: false,
// message}. Unlike post it has no text for a timeout or a rejected token:
// the error itself is shown.
func (c *Client) getOrError(ctx context.Context, endpoint string) *pyfmt.OMap {
	result, _, _, err := c.get(ctx, endpoint, RequestTimeout)
	if err != nil {
		return pyfmt.NewOMap("success", false, "message", fmt.Sprintf("Request error (is the bridge running?): %v", err))
	}
	return result
}

// Outcome is success, the status message and the message id of a bridge answer.
type Outcome struct {
	OK        bool
	Message   string
	MessageID any
}

// OutcomeOf reads success, message and message_id from a bridge answer.
func OutcomeOf(result *pyfmt.OMap) Outcome {
	msg := "Unknown response"
	if result.Has("message") {
		msg = pyfmt.Str(result.Get("message"))
	}
	return Outcome{pyfmt.Truthy(result.Get("success")), msg, result.Get("message_id")}
}

// Fail is a failed outcome with msg.
func Fail(msg string) Outcome { return Outcome{false, msg, nil} }

func (c *Client) act(ctx context.Context, endpoint string, payload *pyfmt.OMap) Outcome {
	return OutcomeOf(c.post(ctx, endpoint, payload, RequestTimeout))
}

// ---------------------------------------------------------------------------
// Sending

// SendMessage sends a text message, optionally as a reply and with mentions.
func (c *Client) SendMessage(ctx context.Context, recipient, message, replyTo string, mentions []string) Outcome {
	if recipient == "" {
		return Fail("Recipient must be provided")
	}
	if message == "" {
		return Fail("Message must be provided")
	}
	payload := pyfmt.NewOMap("recipient", recipient, "message", message)
	if replyTo != "" {
		payload.Set("reply_to_message_id", replyTo)
	}
	if len(mentions) > 0 {
		payload.Set("mentions", mentions)
	}
	return c.act(ctx, "send", payload)
}

// SendReaction reacts to a message ("" removes the reaction).
func (c *Client) SendReaction(ctx context.Context, chatJID, messageID, emoji string) Outcome {
	if chatJID == "" || messageID == "" {
		return Fail("chat_jid and message_id must be provided")
	}
	return c.act(ctx, "react", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID, "emoji", emoji))
}

// SendPoll sends a poll.
func (c *Client) SendPoll(ctx context.Context, recipient, question string, options []string, selectable int) Outcome {
	if recipient == "" {
		return Fail("Recipient must be provided")
	}
	return c.act(ctx, "poll", pyfmt.NewOMap("recipient", recipient, "question", question, "options", nonNil(options), "selectable_count", selectable))
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// SendLocation sends a location pin.
func (c *Client) SendLocation(ctx context.Context, recipient string, lat, lon float64, name, address string) Outcome {
	if recipient == "" {
		return Fail("Recipient must be provided")
	}
	payload := pyfmt.NewOMap("recipient", recipient, "latitude", pyfmt.FloatValue(lat), "longitude", pyfmt.FloatValue(lon))
	if name != "" {
		payload.Set("name", name)
	}
	if address != "" {
		payload.Set("address", address)
	}
	return c.act(ctx, "location", payload)
}

// EditMessage replaces the text of one of your messages.
func (c *Client) EditMessage(ctx context.Context, chatJID, messageID, text string) Outcome {
	if chatJID == "" || messageID == "" {
		return Fail("chat_jid and message_id must be provided")
	}
	if text == "" {
		return Fail("The new text must not be empty (use delete_message to remove a message)")
	}
	return c.act(ctx, "edit", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID, "message", text))
}

// DeleteMessage deletes one of your messages for everyone.
func (c *Client) DeleteMessage(ctx context.Context, chatJID, messageID string) Outcome {
	if chatJID == "" || messageID == "" {
		return Fail("chat_jid and message_id must be provided")
	}
	return c.act(ctx, "delete", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID))
}

// VotePoll votes in a poll.
func (c *Client) VotePoll(ctx context.Context, chatJID, messageID string, options []string) Outcome {
	return c.act(ctx, "poll/vote", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID, "options", nonNil(options)))
}

// SendContact sends a contact card.
func (c *Client) SendContact(ctx context.Context, recipient, name, phone, vcard string) Outcome {
	payload := pyfmt.NewOMap("recipient", recipient, "name", name, "phone", phone)
	if vcard != "" {
		payload.Set("vcard", vcard)
	}
	return c.act(ctx, "contact", payload)
}

// ForwardMessage forwards a message to another chat.
func (c *Client) ForwardMessage(ctx context.Context, chatJID, messageID, recipient string) Outcome {
	return c.act(ctx, "forward", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID, "recipient", recipient))
}

// StarMessage stars or unstars a message.
func (c *Client) StarMessage(ctx context.Context, chatJID, messageID string, starred bool) Outcome {
	return c.act(ctx, "star", pyfmt.NewOMap("chat_jid", chatJID, "message_id", messageID, "starred", starred))
}

// BlockContact blocks or unblocks a contact.
func (c *Client) BlockContact(ctx context.Context, jid string, blocked bool) Outcome {
	return c.act(ctx, "block", pyfmt.NewOMap("jid", jid, "blocked", blocked))
}

// FollowChannel follows or unfollows a channel.
func (c *Client) FollowChannel(ctx context.Context, channel string, follow bool) Outcome {
	return c.act(ctx, "channel/follow", pyfmt.NewOMap("channel", channel, "follow", follow))
}

// MarkRead marks a chat (or some of its messages) as read.
func (c *Client) MarkRead(ctx context.Context, chatJID string, ids []string) Outcome {
	if chatJID == "" {
		return Fail("chat_jid must be provided")
	}
	payload := pyfmt.NewOMap("chat_jid", chatJID)
	if len(ids) > 0 {
		payload.Set("message_ids", ids)
	}
	return c.act(ctx, "read", payload)
}

// SendTyping shows or stops the typing indicator.
func (c *Client) SendTyping(ctx context.Context, chatJID, state string) Outcome {
	if chatJID == "" {
		return Fail("chat_jid must be provided")
	}
	return c.act(ctx, "typing", pyfmt.NewOMap("chat_jid", chatJID, "state", state))
}

// ManageChat archives, pins, mutes or deletes a chat.
func (c *Client) ManageChat(ctx context.Context, chatJID, action string, muteHours *float64) Outcome {
	payload := pyfmt.NewOMap("chat_jid", chatJID, "action", action)
	if muteHours != nil {
		payload.Set("mute_hours", pyfmt.FloatValue(*muteHours))
	}
	return c.act(ctx, "chat", payload)
}

// LabelChat adds or removes a label of a chat.
func (c *Client) LabelChat(ctx context.Context, chatJID, label string, labeled bool) Outcome {
	return c.act(ctx, "label/chat", pyfmt.NewOMap("chat_jid", chatJID, "label", label, "labeled", labeled))
}

// EditLabel creates, renames, recolours or deletes a label.
func (c *Client) EditLabel(ctx context.Context, name, labelID string, color int, deleted bool) Outcome {
	payload := pyfmt.NewOMap("name", name, "color", color, "deleted", deleted)
	if labelID != "" {
		payload.Set("label_id", labelID)
	}
	return c.act(ctx, "label/edit", payload)
}

// CreateGroup creates a group or a community.
func (c *Client) CreateGroup(ctx context.Context, name string, participants []string, community bool) *pyfmt.OMap {
	payload := pyfmt.NewOMap("name", name, "participants", nonNil(participants))
	if community {
		payload.Set("community", true)
	}
	return c.post(ctx, "group/create", payload, RequestTimeout)
}

// LeaveGroup leaves a group.
func (c *Client) LeaveGroup(ctx context.Context, chatJID string) *pyfmt.OMap {
	return c.post(ctx, "group/leave", pyfmt.NewOMap("chat_jid", chatJID), RequestTimeout)
}

// UpdateGroupParticipants adds, removes, promotes or demotes members.
func (c *Client) UpdateGroupParticipants(ctx context.Context, chatJID, action string, participants []string) *pyfmt.OMap {
	return c.post(ctx, "group/participants", pyfmt.NewOMap("chat_jid", chatJID, "action", action, "participants", nonNil(participants)), RequestTimeout)
}

// GroupSettings are the optional fields of update_group_settings; only
// given ones change.
type GroupSettings struct {
	Name, Description                  *string
	AnnounceOnly, Locked, JoinApproval *bool
}

// UpdateGroupSettings changes the given settings of a group.
func (c *Client) UpdateGroupSettings(ctx context.Context, chatJID string, s GroupSettings) *pyfmt.OMap {
	payload := pyfmt.NewOMap("chat_jid", chatJID)
	if s.Name != nil {
		payload.Set("name", *s.Name)
	}
	if s.Description != nil {
		payload.Set("description", *s.Description)
	}
	if s.AnnounceOnly != nil {
		payload.Set("announce_only", *s.AnnounceOnly)
	}
	if s.Locked != nil {
		payload.Set("locked", *s.Locked)
	}
	if s.JoinApproval != nil {
		payload.Set("join_approval", *s.JoinApproval)
	}
	return c.post(ctx, "group/settings", payload, RequestTimeout)
}

// JoinGroup joins a group by invite link or code.
func (c *Client) JoinGroup(ctx context.Context, invite string) *pyfmt.OMap {
	return c.post(ctx, "group/join", pyfmt.NewOMap("invite", invite), RequestTimeout)
}

// UpdateGroupJoinRequests approves or rejects requests to join.
func (c *Client) UpdateGroupJoinRequests(ctx context.Context, chatJID, action string, participants []string) *pyfmt.OMap {
	return c.post(ctx, "group/requests/update", pyfmt.NewOMap("chat_jid", chatJID, "action", action, "participants", nonNil(participants)), RequestTimeout)
}

// ResetGroupInviteLink revokes a group's invite link and makes a new one.
func (c *Client) ResetGroupInviteLink(ctx context.Context, chatJID string) *pyfmt.OMap {
	return c.post(ctx, "group/invite/reset", pyfmt.NewOMap("chat_jid", chatJID), RequestTimeout)
}

// SetDisappearingMessages sets a chat's disappearing-message timer.
func (c *Client) SetDisappearingMessages(ctx context.Context, chatJID, timer string) Outcome {
	return c.act(ctx, "chat/disappearing", pyfmt.NewOMap("chat_jid", chatJID, "timer", timer))
}

// LinkCommunityGroup links a group to a community, or unlinks it.
func (c *Client) LinkCommunityGroup(ctx context.Context, communityJID, groupJID string, unlink bool) *pyfmt.OMap {
	payload := pyfmt.NewOMap("community_jid", communityJID, "group_jid", groupJID)
	if unlink {
		payload.Set("unlink", true)
	}
	return c.post(ctx, "community/link", payload, RequestTimeout)
}

// Status is the bridge's /api/status, plus a `warning` when the
// WhatsApp session is lost (logged out, QR code needed, replaced by another
// client) and no messages will arrive until someone fixes it.
func (c *Client) Status(ctx context.Context) *pyfmt.OMap {
	resp, cancel, err := c.request(ctx, http.MethodGet, "status", nil, StatusTimeout)
	if err != nil {
		return pyfmt.NewOMap("connected", false, "error", fmt.Sprintf("Bridge not reachable: %v", err))
	}
	defer cancel()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return pyfmt.NewOMap("connected", false, "error", fmt.Sprintf("Bridge not reachable: %v", err))
	}
	if resp.StatusCode != http.StatusOK {
		return pyfmt.NewOMap("connected", false, "error", fmt.Sprintf("HTTP %d - %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	decoded, err := pyfmt.DecodeOrdered(data)
	status, ok := decoded.(*pyfmt.OMap)
	if err != nil || !ok {
		return pyfmt.NewOMap("connected", false, "error", fmt.Sprintf("HTTP %d - %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	session, _ := status.Get("session").(*pyfmt.OMap)
	if session != nil && pyfmt.Truthy(session.Get("needs_action")) {
		reason := ""
		if session.Has("reason") {
			reason = pyfmt.Str(session.Get("reason"))
		}
		status.Set("warning", fmt.Sprintf("WhatsApp session %s: %s. No messages are received or sent until this is fixed.",
			pyfmt.Str(session.Get("state")), reason))
	}
	return status
}

// ReconnectBridge asks the bridge to drop and reopen its WhatsApp
// connection. The bridge allows this at most every 30 seconds and only for
// a paired device.
func (c *Client) ReconnectBridge(ctx context.Context) Outcome {
	o := c.act(ctx, "connection/reconnect", pyfmt.NewOMap())
	o.MessageID = nil
	return o
}

// ContactInfo asks the bridge about a contact (and its picture).
func (c *Client) ContactInfo(ctx context.Context, jid string, picture bool) *pyfmt.OMap {
	return c.post(ctx, "contact/info", pyfmt.NewOMap("jid", jid, "picture", picture), RequestTimeout)
}

// Blocklist is the bridge's list of blocked contacts.
func (c *Client) Blocklist(ctx context.Context) *pyfmt.OMap { return c.getOrError(ctx, "blocklist") }

// Presence asks for a contact's presence.
func (c *Client) Presence(ctx context.Context, jid string) *pyfmt.OMap {
	return c.post(ctx, "presence", pyfmt.NewOMap("jid", jid), RequestTimeout)
}

// Channels lists the followed channels.
func (c *Client) Channels(ctx context.Context) *pyfmt.OMap { return c.getOrError(ctx, "channels") }

// ChannelMessages fetches a channel's messages.
func (c *Client) ChannelMessages(ctx context.Context, channelJID string, count, beforeServerID int) *pyfmt.OMap {
	return c.post(ctx, "channel/messages", pyfmt.NewOMap("channel_jid", channelJID, "count", count, "before_server_id", beforeServerID), RequestTimeout)
}

// GroupInfo asks WhatsApp for a group's name, description, members with
// names and admin roles, and the invite link when you are an admin.
func (c *Client) GroupInfo(ctx context.Context, chatJID string, inviteLink bool) *pyfmt.OMap {
	return c.post(ctx, "group", pyfmt.NewOMap("chat_jid", chatJID, "invite_link", inviteLink), RequestTimeout)
}

// GroupJoinRequests lists the pending requests to join a group.
func (c *Client) GroupJoinRequests(ctx context.Context, chatJID string) *pyfmt.OMap {
	return c.post(ctx, "group/requests", pyfmt.NewOMap("chat_jid", chatJID), RequestTimeout)
}

// CommunityGroups lists the groups of a community.
func (c *Client) CommunityGroups(ctx context.Context, communityJID string) *pyfmt.OMap {
	return c.post(ctx, "community/groups", pyfmt.NewOMap("community_jid", communityJID), RequestTimeout)
}

// History asks the phone for older messages of a chat.
func (c *Client) History(ctx context.Context, chatJID string, count int, beforeID string) Outcome {
	payload := pyfmt.NewOMap("chat_jid", chatJID, "count", count)
	if beforeID != "" {
		payload.Set("before_message_id", beforeID)
	}
	o := c.act(ctx, "history", payload)
	o.MessageID = nil
	return o
}

// Download downloads a message's media through the bridge. Expired media
// is re-requested from the phone, which can take up to a minute.
func (c *Client) Download(ctx context.Context, messageID, chatJID string) *pyfmt.OMap {
	return c.post(ctx, "download", pyfmt.NewOMap("message_id", messageID, "chat_jid", chatJID), DownloadTimeout)
}

// Send posts a message to /api/send.
func (c *Client) Send(ctx context.Context, payload *pyfmt.OMap) Outcome {
	return c.act(ctx, "send", payload)
}

// Sticker posts a sticker to /api/sticker.
func (c *Client) Sticker(ctx context.Context, payload *pyfmt.OMap) Outcome {
	return c.act(ctx, "sticker", payload)
}

// Events calls fn with bridge events (message, edit, delete, reaction,
// history, session) as they arrive, until timeout passes, ctx ends or fn
// returns false. Silence longer than the read timeout ends the stream.
//
// filter says which chats this process may see (a nil func: every chat).
func (c *Client) Events(ctx context.Context, timeout time.Duration, chatJID string, filter Filter, fn func(*pyfmt.OMap) bool) error {
	var allow func(string) bool
	if filter != nil {
		var err error
		if allow, err = filter(ctx); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, cancelReq, err := c.request(ctx, http.MethodGet, "events", nil, timeout+time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer cancelReq()
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%d %s for url: %s (%s)", resp.StatusCode, http.StatusText(resp.StatusCode), resp.Request.URL, strings.TrimSpace(string(body)))
	}
	readTimeout := min(30*time.Second, timeout+time.Second)
	lines := readLines(ctx, resp.Body)
	idle := time.NewTimer(readTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-idle.C:
			// The read timeout ends the stream when nothing arrives.
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			idle.Reset(readTimeout)
			more, err := dispatch(line, chatJID, allow, fn)
			if err != nil || !more {
				return err
			}
		}
	}
}

// readLines sends the lines of body until it ends or ctx does.
func readLines(ctx context.Context, body io.Reader) <-chan string {
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines
}

// dispatch passes the event of an SSE line to fn when it concerns chatJID
// ("": every chat) and allow lets it through; more is false once fn
// wants no more. An undecodable event is an error, which ends the stream.
func dispatch(line, chatJID string, allow func(string) bool, fn func(*pyfmt.OMap) bool) (more bool, err error) {
	data, found := strings.CutPrefix(line, "data: ")
	if !found {
		return true, nil
	}
	decoded, err := pyfmt.DecodeOrdered([]byte(data))
	if err != nil {
		return false, err
	}
	event, ok := decoded.(*pyfmt.OMap)
	if !ok {
		return true, nil
	}
	eventChat, _ := event.Get("chat_jid").(string)
	// Session events concern every chat: a watcher of one chat must still
	// learn that the session was lost.
	if chatJID != "" && eventChat != chatJID && event.Get("type") != "session" {
		return true, nil
	}
	// The bridge filters by its own read allowlist; this process's list is
	// applied here.
	if eventChat != "" && allow != nil && !allow(eventChat) {
		return true, nil
	}
	return fn(event), nil
}

// WaitForEvents waits for new activity and returns it as soon as at least
// one event arrived and no more followed for two seconds, or at the timeout.
func (c *Client) WaitForEvents(ctx context.Context, timeoutSeconds int, chatJID string, maxEvents int, filter Filter) *pyfmt.OMap {
	timeoutSeconds = max(1, min(timeoutSeconds, 600))
	maxEvents = max(1, maxEvents)
	ctx, cancel := context.WithCancel(ctx)
	// Cancelled when this call returns, so the reader closes its
	// connection to the bridge instead of holding it open.
	defer cancel()
	received := make(chan *pyfmt.OMap)
	done := make(chan error, 1)
	go func() {
		done <- c.Events(ctx, time.Duration(timeoutSeconds)*time.Second, chatJID, filter, func(ev *pyfmt.OMap) bool {
			select {
			case received <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	events := []any{}
	started := time.Now()
	deadline := started.Add(time.Duration(timeoutSeconds) * time.Second)
	waited := func() int { return int(math.RoundToEven(time.Since(started).Seconds())) }
	for len(events) < maxEvents {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if len(events) > 0 {
			remaining = min(remaining, 2*time.Second)
		}
		timer := time.NewTimer(remaining)
		select {
		case ev := <-received:
			timer.Stop()
			events = append(events, ev)
			continue
		case err := <-done:
			timer.Stop()
			if err != nil {
				return pyfmt.NewOMap("success", false, "message", fmt.Sprintf("Event stream failed: %v", err), "events", events)
			}
			return pyfmt.NewOMap("success", true, "waited_seconds", waited(), "events", events)
		case <-timer.C:
		case <-ctx.Done():
		}
		if len(events) > 0 || ctx.Err() != nil {
			break
		}
	}
	return pyfmt.NewOMap("success", true, "waited_seconds", waited(), "events", events)
}
