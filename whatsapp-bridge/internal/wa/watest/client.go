package watest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// SentAt is the time stamp every send answers with.
var SentAt = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

// Client is a fake wa.Client. The embedded interface stays nil: a method
// this type does not implement panics when called (the stack trace names
// it), and an implemented one whose behaviour needs a func a test did not
// set panics with "watest: <method> called without <field>".
//
// The zero value is offline; New returns a connected, linked account. All
// methods are safe for concurrent use as long as the exported fields are set
// before the client is shared.
type Client struct {
	wa.Client

	Connected bool
	LoggedIn  bool
	ID        *types.JID // own phone-number JID; nil while not linked
	LID       types.JID
	Name      string // push name
	LIDMap    *LIDMap
	Book      *ContactBook
	// Groups answers GetGroupInfo; a missing group is an error.
	Groups map[types.JID]*types.GroupInfo
	// ParticipantErrors are the error codes UpdateGroupParticipants
	// reports per participant.
	ParticipantErrors map[types.JID]int
	// InviteLinkErr fails GetGroupInviteLink.
	InviteLinkErr error
	// Blocked is the blocklist; About the status texts GetUserInfo reports.
	Blocked []types.JID
	About   map[types.JID]string

	// Overrides. A nil func gives the default noted on its method.
	SendMessageFunc       func(ctx context.Context, to types.JID, msg *waE2E.Message) (whatsmeow.SendResponse, error)
	UploadFunc            func(ctx context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	DownloadFunc          func(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)
	MediaRetryFunc        func(ctx context.Context, info *types.MessageInfo, mediaKey []byte) error
	PollVoteFunc          func(ctx context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error)
	DecryptReactionFunc   func(ctx context.Context, evt *events.Message) (*waE2E.ReactionMessage, error)
	DecryptSecretFunc     func(ctx context.Context, evt *events.Message) (*waE2E.Message, error)
	DecryptPollVoteFunc   func(ctx context.Context, evt *events.Message) (*waE2E.PollVoteMessage, error)
	MessageSecretFunc     func(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error)
	SendIQFunc            func(ctx context.Context, query whatsmeow.DangerousInfoQuery) (*waBinary.Node, error)
	GroupFromLinkFunc     func(ctx context.Context, code string) (*types.GroupInfo, error)
	JoinWithLinkFunc      func(ctx context.Context, code string) (types.JID, error)
	PictureFunc           func(ctx context.Context, jid types.JID) (*types.ProfilePictureInfo, error)
	SetGroupFunc          func(method string, group types.JID) error
	SubscribePresenceFunc func(ctx context.Context, jid types.JID) error

	mu    sync.Mutex
	calls []string
	sent  []Sent
	next  int
}

// Sent is one message handed to SendMessage or SendPeerMessage (To empty).
type Sent struct {
	To      types.JID
	Message *waE2E.Message
}

// New is a connected client linked as own (a phone-number JID) with the LID
// lid, named "Me", with empty LID and contact stores.
func New(own, lid types.JID) *Client {
	return &Client{
		Connected: true, LoggedIn: true, ID: &own, LID: lid, Name: "Me",
		LIDMap: NewLIDMap(), Book: NewContactBook(), Groups: map[types.JID]*types.GroupInfo{},
	}
}

var _ wa.Client = (*Client)(nil)

func (c *Client) record(call string) {
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
}

// Calls lists the methods called so far, in order (one entry per call of a
// method this type implements).
func (c *Client) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

// Sent lists the messages sent so far.
func (c *Client) Sent() []Sent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Sent(nil), c.sent...)
}

func missing(method, field string) string {
	return fmt.Sprintf("watest: %s called without %s", method, field)
}

// IsConnected implements wa.Session.
func (c *Client) IsConnected() bool { return c.Connected }

// IsLoggedIn implements wa.Session.
func (c *Client) IsLoggedIn() bool { return c.LoggedIn }

// OwnID implements wa.Session.
func (c *Client) OwnID() *types.JID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ID
}

// OwnLID implements wa.Session.
func (c *Client) OwnLID() types.JID { return c.LID }

// PushName implements wa.Session.
func (c *Client) PushName() string { return c.Name }

// LIDs implements wa.Session; nil without a LIDMap.
func (c *Client) LIDs() wa.LIDStore {
	if c.LIDMap == nil {
		return nil
	}
	return c.LIDMap
}

// Contacts implements wa.Session; nil without a Book.
func (c *Client) Contacts() wa.ContactStore {
	if c.Book == nil {
		return nil
	}
	return c.Book
}

// Logout implements wa.Session: it succeeds and forgets the own JID.
func (c *Client) Logout(context.Context) error {
	c.record("Logout")
	c.mu.Lock()
	c.ID = nil
	c.mu.Unlock()
	return nil
}

// MessageSecret implements wa.Session; without MessageSecretFunc no secret
// is known.
func (c *Client) MessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
	c.record("MessageSecret")
	if c.MessageSecretFunc == nil {
		return nil, types.EmptyJID, nil
	}
	return c.MessageSecretFunc(ctx, chat, sender, id)
}

// SendMessage implements wa.Messenger: it records the message and answers
// with the ids SENT1, SENT2, … at SentAt, unless SendMessageFunc decides.
func (c *Client) SendMessage(ctx context.Context, to types.JID, msg *waE2E.Message, _ ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	c.record("SendMessage")
	if c.SendMessageFunc != nil {
		resp, err := c.SendMessageFunc(ctx, to, msg)
		if err == nil {
			c.mu.Lock()
			c.sent = append(c.sent, Sent{To: to, Message: msg})
			c.mu.Unlock()
		}
		return resp, err
	}
	return c.store(to, msg), nil
}

func (c *Client) store(to types.JID, msg *waE2E.Message) whatsmeow.SendResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, Sent{To: to, Message: msg})
	c.next++
	return whatsmeow.SendResponse{ID: fmt.Sprintf("SENT%d", c.next), Timestamp: SentAt}
}

// SendPeerMessage implements wa.Messenger; it records the message.
func (c *Client) SendPeerMessage(_ context.Context, msg *waE2E.Message) (whatsmeow.SendResponse, error) {
	c.record("SendPeerMessage")
	return c.store(types.EmptyJID, msg), nil
}

// Upload implements wa.Messenger. By default the upload succeeds with
// hashes of data and a fake CDN location.
func (c *Client) Upload(ctx context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	c.record("Upload")
	if c.UploadFunc != nil {
		return c.UploadFunc(ctx, data, kind)
	}
	sum := sha256.Sum256(data)
	return whatsmeow.UploadResponse{
		URL: "https://mmg.example/" + string(kind), DirectPath: "/v/" + string(kind),
		MediaKey: sum[:], FileSHA256: sum[:], FileEncSHA256: sum[:], FileLength: uint64(len(data)),
	}, nil
}

// Download implements wa.Messenger through DownloadFunc.
func (c *Client) Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	c.record("Download")
	if c.DownloadFunc == nil {
		panic(missing("Download", "DownloadFunc"))
	}
	return c.DownloadFunc(ctx, msg)
}

// SendMediaRetryReceipt implements wa.Messenger through MediaRetryFunc.
func (c *Client) SendMediaRetryReceipt(ctx context.Context, info *types.MessageInfo, mediaKey []byte) error {
	c.record("SendMediaRetryReceipt")
	if c.MediaRetryFunc == nil {
		panic(missing("SendMediaRetryReceipt", "MediaRetryFunc"))
	}
	return c.MediaRetryFunc(ctx, info, mediaKey)
}

func key(chat, sender types.JID, id types.MessageID, fromMe bool) *waCommon.MessageKey {
	k := &waCommon.MessageKey{RemoteJID: new(chat.String()), ID: new(id), FromMe: new(fromMe)}
	if !sender.IsEmpty() {
		k.Participant = new(sender.String())
	}
	return k
}

// BuildReaction implements wa.Messenger with a plain reaction message.
func (c *Client) BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message {
	return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: key(chat, sender, id, false), Text: new(reaction)}}
}

// BuildEdit implements wa.Messenger with a protocol edit message.
func (c *Client) BuildEdit(chat types.JID, id types.MessageID, newContent *waE2E.Message) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: key(chat, types.EmptyJID, id, true), EditedMessage: newContent,
	}}
}

// BuildRevoke implements wa.Messenger with a protocol revoke message.
func (c *Client) BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: key(chat, sender, id, sender.IsEmpty()),
	}}
}

// BuildPollCreation implements wa.Messenger with an unencrypted poll.
func (c *Client) BuildPollCreation(name string, optionNames []string, selectable int) *waE2E.Message {
	poll := &waE2E.PollCreationMessage{Name: new(name), SelectableOptionsCount: new(uint32(selectable))} //nolint:gosec // test data
	for _, o := range optionNames {
		poll.Options = append(poll.Options, &waE2E.PollCreationMessage_Option{OptionName: new(o)})
	}
	return &waE2E.Message{PollCreationMessageV3: poll}
}

// BuildPollVote implements wa.Messenger through PollVoteFunc.
func (c *Client) BuildPollVote(ctx context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error) {
	c.record("BuildPollVote")
	if c.PollVoteFunc == nil {
		panic(missing("BuildPollVote", "PollVoteFunc"))
	}
	return c.PollVoteFunc(ctx, poll, options)
}

// BuildHistorySyncRequest implements wa.Messenger with a peer history
// request naming the oldest known message.
func (c *Client) BuildHistorySyncRequest(last *types.MessageInfo, count int) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(),
		PeerDataOperationRequestMessage: &waE2E.PeerDataOperationRequestMessage{
			HistorySyncOnDemandRequest: &waE2E.PeerDataOperationRequestMessage_HistorySyncOnDemandRequest{
				ChatJID: new(last.Chat.String()), OldestMsgID: new(last.ID), OnDemandMsgCount: new(int32(count)), //nolint:gosec // test data
			},
		},
	}}
}

// DecryptReaction implements wa.Messenger through DecryptReactionFunc.
func (c *Client) DecryptReaction(ctx context.Context, evt *events.Message) (*waE2E.ReactionMessage, error) {
	if c.DecryptReactionFunc == nil {
		panic(missing("DecryptReaction", "DecryptReactionFunc"))
	}
	return c.DecryptReactionFunc(ctx, evt)
}

// DecryptSecretEncryptedMessage implements wa.Messenger through
// DecryptSecretFunc.
func (c *Client) DecryptSecretEncryptedMessage(ctx context.Context, evt *events.Message) (*waE2E.Message, error) {
	if c.DecryptSecretFunc == nil {
		panic(missing("DecryptSecretEncryptedMessage", "DecryptSecretFunc"))
	}
	return c.DecryptSecretFunc(ctx, evt)
}

// DecryptPollVote implements wa.Messenger through DecryptPollVoteFunc.
func (c *Client) DecryptPollVote(ctx context.Context, evt *events.Message) (*waE2E.PollVoteMessage, error) {
	if c.DecryptPollVoteFunc == nil {
		panic(missing("DecryptPollVote", "DecryptPollVoteFunc"))
	}
	return c.DecryptPollVoteFunc(ctx, evt)
}

// ParseWebMessage implements wa.Messenger for history sync: the sender is
// the key's participant, the chat itself in a direct chat, or the own JID
// for our messages.
func (c *Client) ParseWebMessage(chat types.JID, web *waWeb.WebMessageInfo) (*events.Message, error) {
	k := web.GetKey()
	if k.GetID() == "" {
		return nil, errors.New("watest: history message without id")
	}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: k.GetFromMe(), IsGroup: chat.Server == types.GroupServer},
		ID:            k.GetID(),
		Timestamp:     time.Unix(int64(web.GetMessageTimestamp()), 0), //nolint:gosec // test data
		PushName:      web.GetPushName(),
	}
	switch own := c.OwnID(); {
	case k.GetFromMe() && own != nil:
		info.Sender = own.ToNonAD()
	case web.GetParticipant() != "" || k.GetParticipant() != "":
		p := web.GetParticipant()
		if p == "" {
			p = k.GetParticipant()
		}
		sender, err := types.ParseJID(p)
		if err != nil {
			return nil, err
		}
		info.Sender = sender
	default:
		info.Sender = chat
	}
	return &events.Message{Info: info, Message: web.GetMessage(), SourceWebMsg: web}, nil
}

// MarkRead implements wa.Messenger; it succeeds.
func (c *Client) MarkRead(context.Context, []types.MessageID, time.Time, types.JID, types.JID, ...types.ReceiptType) error {
	c.record("MarkRead")
	return nil
}

// SendChatPresence implements wa.Messenger; it succeeds.
func (c *Client) SendChatPresence(context.Context, types.JID, types.ChatPresence, types.ChatPresenceMedia) error {
	c.record("SendChatPresence")
	return nil
}

// SendPresence implements wa.Messenger; it succeeds.
func (c *Client) SendPresence(context.Context, types.Presence) error {
	c.record("SendPresence")
	return nil
}

// SubscribePresence implements wa.Messenger; it succeeds unless
// SubscribePresenceFunc decides.
func (c *Client) SubscribePresence(ctx context.Context, jid types.JID) error {
	c.record("SubscribePresence")
	if c.SubscribePresenceFunc != nil {
		return c.SubscribePresenceFunc(ctx, jid)
	}
	return nil
}

// RejectCall implements wa.Messenger; it succeeds.
func (c *Client) RejectCall(context.Context, types.JID, string) error {
	c.record("RejectCall")
	return nil
}

// GetGroupInfo implements wa.Groups from Groups.
func (c *Client) GetGroupInfo(_ context.Context, jid types.JID) (*types.GroupInfo, error) {
	c.record("GetGroupInfo")
	if info, ok := c.Groups[jid]; ok {
		return info, nil
	}
	return nil, whatsmeow.ErrGroupNotFound
}

// GetGroupInfoFromLink implements wa.Groups through GroupFromLinkFunc.
func (c *Client) GetGroupInfoFromLink(ctx context.Context, code string) (*types.GroupInfo, error) {
	c.record("GetGroupInfoFromLink")
	if c.GroupFromLinkFunc == nil {
		panic(missing("GetGroupInfoFromLink", "GroupFromLinkFunc"))
	}
	return c.GroupFromLinkFunc(ctx, code)
}

// JoinGroupWithLink implements wa.Groups through JoinWithLinkFunc.
func (c *Client) JoinGroupWithLink(ctx context.Context, code string) (types.JID, error) {
	c.record("JoinGroupWithLink")
	if c.JoinWithLinkFunc == nil {
		panic(missing("JoinGroupWithLink", "JoinWithLinkFunc"))
	}
	return c.JoinWithLinkFunc(ctx, code)
}

func (c *Client) setGroup(method string, group types.JID) error {
	c.record(method)
	if c.SetGroupFunc == nil {
		return nil
	}
	return c.SetGroupFunc(method, group)
}

// SetGroupName implements wa.Groups; SetGroupFunc may fail it.
func (c *Client) SetGroupName(_ context.Context, jid types.JID, _ string) error {
	return c.setGroup("SetGroupName", jid)
}

// SetGroupTopic implements wa.Groups; SetGroupFunc may fail it.
func (c *Client) SetGroupTopic(_ context.Context, jid types.JID, _, _, _ string) error {
	return c.setGroup("SetGroupTopic", jid)
}

// SetGroupAnnounce implements wa.Groups; SetGroupFunc may fail it.
func (c *Client) SetGroupAnnounce(_ context.Context, jid types.JID, _ bool) error {
	return c.setGroup("SetGroupAnnounce", jid)
}

// SetGroupLocked implements wa.Groups; SetGroupFunc may fail it.
func (c *Client) SetGroupLocked(_ context.Context, jid types.JID, _ bool) error {
	return c.setGroup("SetGroupLocked", jid)
}

// SetGroupJoinApprovalMode implements wa.Groups; SetGroupFunc may fail it.
func (c *Client) SetGroupJoinApprovalMode(_ context.Context, jid types.JID, _ bool) error {
	return c.setGroup("SetGroupJoinApprovalMode", jid)
}

// GetProfilePictureInfo implements wa.Contacts through PictureFunc.
func (c *Client) GetProfilePictureInfo(ctx context.Context, jid types.JID, _ *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
	c.record("GetProfilePictureInfo")
	if c.PictureFunc == nil {
		panic(missing("GetProfilePictureInfo", "PictureFunc"))
	}
	return c.PictureFunc(ctx, jid)
}

// SendIQ implements wa.Newsletters through SendIQFunc.
func (c *Client) SendIQ(ctx context.Context, query whatsmeow.DangerousInfoQuery) (*waBinary.Node, error) {
	c.record("SendIQ")
	if c.SendIQFunc == nil {
		panic(missing("SendIQ", "SendIQFunc"))
	}
	return c.SendIQFunc(ctx, query)
}

// SendAppState implements wa.AppState; it succeeds.
func (c *Client) SendAppState(context.Context, appstate.PatchInfo) error {
	c.record("SendAppState")
	return nil
}

// GetGroupInviteLink implements wa.Groups: a link built from the group's
// user part, or the error in InviteLinkErr.
func (c *Client) GetGroupInviteLink(_ context.Context, jid types.JID, reset bool) (string, error) {
	c.record("GetGroupInviteLink")
	if c.InviteLinkErr != nil {
		return "", c.InviteLinkErr
	}
	link := "https://chat.whatsapp.com/" + jid.User
	if reset {
		link += "-new"
	}
	return link, nil
}

// CreateGroup implements wa.Groups: the group is added to Groups with the
// participants as members.
func (c *Client) CreateGroup(_ context.Context, req whatsmeow.ReqCreateGroup) (*types.GroupInfo, error) {
	c.record("CreateGroup")
	info := &types.GroupInfo{JID: types.NewJID("120363000000000001", types.GroupServer), GroupName: types.GroupName{Name: req.Name}}
	for _, p := range req.Participants {
		info.Participants = append(info.Participants, types.GroupParticipant{JID: p})
	}
	if c.Groups == nil {
		c.Groups = map[types.JID]*types.GroupInfo{}
	}
	c.Groups[info.JID] = info
	return info, nil
}

// LeaveGroup implements wa.Groups; it succeeds.
func (c *Client) LeaveGroup(context.Context, types.JID) error {
	c.record("LeaveGroup")
	return nil
}

// UpdateGroupParticipants implements wa.Groups: every change succeeds
// unless ParticipantErrors names an error code for the participant.
func (c *Client) UpdateGroupParticipants(_ context.Context, _ types.JID, changes []types.JID, _ whatsmeow.ParticipantChange) ([]types.GroupParticipant, error) {
	c.record("UpdateGroupParticipants")
	out := make([]types.GroupParticipant, 0, len(changes))
	for _, p := range changes {
		out = append(out, types.GroupParticipant{JID: p, Error: c.ParticipantErrors[p]})
	}
	return out, nil
}

// IsOnWhatsApp implements wa.Contacts: every number is on WhatsApp.
func (c *Client) IsOnWhatsApp(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	c.record("IsOnWhatsApp")
	out := make([]types.IsOnWhatsAppResponse, 0, len(phones))
	for _, p := range phones {
		out = append(out, types.IsOnWhatsAppResponse{Query: p, IsIn: true})
	}
	return out, nil
}

// GetUserInfo implements wa.Contacts from About (status texts by JID).
func (c *Client) GetUserInfo(_ context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
	c.record("GetUserInfo")
	out := map[types.JID]types.UserInfo{}
	for _, j := range jids {
		if about, ok := c.About[j]; ok {
			out[j] = types.UserInfo{Status: about}
		}
	}
	return out, nil
}

// GetBlocklist implements wa.Contacts from Blocked.
func (c *Client) GetBlocklist(context.Context, string) (*types.Blocklist, error) {
	c.record("GetBlocklist")
	c.mu.Lock()
	defer c.mu.Unlock()
	list := &types.Blocklist{}
	for _, j := range c.Blocked {
		list.Items = append(list.Items, types.BlocklistItem{PN: j})
	}
	return list, nil
}

// UpdateBlocklist implements wa.Contacts by changing Blocked.
func (c *Client) UpdateBlocklist(ctx context.Context, jid types.JID, action events.BlocklistChangeAction, _ string) (*types.Blocklist, error) {
	c.record("UpdateBlocklist")
	c.mu.Lock()
	kept := c.Blocked[:0]
	for _, j := range c.Blocked {
		if j != jid {
			kept = append(kept, j)
		}
	}
	c.Blocked = kept
	if action == events.BlocklistChangeActionBlock {
		c.Blocked = append(c.Blocked, jid)
	}
	c.mu.Unlock()
	return c.GetBlocklist(ctx, "")
}
