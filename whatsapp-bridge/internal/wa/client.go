package wa

import (
	"context"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Client is what the bridge asks of WhatsApp: exactly the *whatsmeow.Client
// methods it calls, plus accessors for the device store. New adapts a real
// client; tests can provide a fake.
type Client interface {
	Messenger
	Groups
	Contacts
	Newsletters
	AppState
	Session
}

// Messenger sends, builds, decrypts and acknowledges messages.
type Messenger interface {
	// Sending.
	SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
	SendPeerMessage(ctx context.Context, message *waE2E.Message) (whatsmeow.SendResponse, error)
	Upload(ctx context.Context, plaintext []byte, appInfo whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)
	SendMediaRetryReceipt(ctx context.Context, message *types.MessageInfo, mediaKey []byte) error

	// Builders.
	BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message
	BuildEdit(chat types.JID, id types.MessageID, newContent *waE2E.Message) *waE2E.Message
	BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message
	BuildPollCreation(name string, optionNames []string, selectableOptionCount int) *waE2E.Message
	BuildPollVote(ctx context.Context, pollInfo *types.MessageInfo, optionNames []string) (*waE2E.Message, error)
	BuildHistorySyncRequest(lastKnownMessageInfo *types.MessageInfo, count int) *waE2E.Message

	// Decrypting and parsing.
	DecryptReaction(ctx context.Context, reaction *events.Message) (*waE2E.ReactionMessage, error)
	DecryptSecretEncryptedMessage(ctx context.Context, evt *events.Message) (*waE2E.Message, error)
	DecryptPollVote(ctx context.Context, vote *events.Message) (*waE2E.PollVoteMessage, error)
	ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)

	// Receipts and presence.
	MarkRead(ctx context.Context, ids []types.MessageID, timestamp time.Time, chat, sender types.JID, receiptTypeExtra ...types.ReceiptType) error
	SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error
	SendPresence(ctx context.Context, state types.Presence) error
	SubscribePresence(ctx context.Context, jid types.JID) error
	RejectCall(ctx context.Context, callFrom types.JID, callID string) error
}

// Groups reads and manages groups and communities.
type Groups interface {
	// Info and membership.
	GetGroupInfo(ctx context.Context, jid types.JID) (*types.GroupInfo, error)
	GetGroupInfoFromLink(ctx context.Context, code string) (*types.GroupInfo, error)
	JoinGroupWithLink(ctx context.Context, code string) (types.JID, error)
	CreateGroup(ctx context.Context, req whatsmeow.ReqCreateGroup) (*types.GroupInfo, error)
	LeaveGroup(ctx context.Context, jid types.JID) error
	UpdateGroupParticipants(ctx context.Context, jid types.JID, participantChanges []types.JID, action whatsmeow.ParticipantChange) ([]types.GroupParticipant, error)

	// Join requests.
	GetGroupRequestParticipants(ctx context.Context, jid types.JID) ([]types.GroupParticipantRequest, error)
	UpdateGroupRequestParticipants(ctx context.Context, jid types.JID, participantChanges []types.JID, action whatsmeow.ParticipantRequestChange) ([]types.GroupParticipant, error)

	// Settings.
	SetGroupName(ctx context.Context, jid types.JID, name string) error
	SetGroupTopic(ctx context.Context, jid types.JID, previousID, newID, topic string) error
	SetGroupAnnounce(ctx context.Context, jid types.JID, announce bool) error
	SetGroupLocked(ctx context.Context, jid types.JID, locked bool) error
	SetGroupJoinApprovalMode(ctx context.Context, jid types.JID, mode bool) error
	SetDisappearingTimer(ctx context.Context, chat types.JID, timer time.Duration, settingTS time.Time) error
	GetGroupInviteLink(ctx context.Context, jid types.JID, reset bool) (string, error)

	// Communities.
	LinkGroup(ctx context.Context, parent, child types.JID) error
	UnlinkGroup(ctx context.Context, parent, child types.JID) error
	GetSubGroups(ctx context.Context, community types.JID) ([]*types.GroupLinkTarget, error)
}

// Contacts looks people up and manages the blocklist.
type Contacts interface {
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error)
	GetProfilePictureInfo(ctx context.Context, jid types.JID, params *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error)
	GetBlocklist(ctx context.Context, dhash string) (*types.Blocklist, error)
	UpdateBlocklist(ctx context.Context, jid types.JID, action events.BlocklistChangeAction, dhash string) (*types.Blocklist, error)
}

// Newsletters reads and follows channels.
type Newsletters interface {
	GetNewsletterInfo(ctx context.Context, jid types.JID) (*types.NewsletterMetadata, error)
	GetNewsletterInfoWithInvite(ctx context.Context, key string) (*types.NewsletterMetadata, error)
	GetNewsletterMessages(ctx context.Context, jid types.JID, params *whatsmeow.GetNewsletterMessagesParams) ([]*types.NewsletterMessage, error)
	FollowNewsletter(ctx context.Context, jid types.JID) error
	UnfollowNewsletter(ctx context.Context, jid types.JID) error
	// SendIQ sends a raw info query. The followed-channels list needs it:
	// whatsmeow's own call still sends an outdated query id.
	SendIQ(ctx context.Context, query whatsmeow.DangerousInfoQuery) (*waBinary.Node, error)
}

// AppState changes state that syncs to every device of the account:
// archive, pin, mute, delete, labels, stars.
type AppState interface {
	SendAppState(ctx context.Context, patch appstate.PatchInfo) error
}

// Session is the account's connection and what the device store knows about
// it.
type Session interface {
	IsConnected() bool
	IsLoggedIn() bool
	Logout(ctx context.Context) error

	// OwnID is the account's phone-number JID with device, nil until the
	// device is linked.
	OwnID() *types.JID
	// OwnLID is the account's LID, empty until known.
	OwnLID() types.JID
	// PushName is the account's own display name.
	PushName() string
	// LIDs maps between LIDs and phone numbers; nil without a device store.
	LIDs() LIDStore
	// Contacts are the address book and push names; nil without a device
	// store.
	Contacts() ContactStore
	// MessageSecret is the secret a message was sent with, which decrypts
	// votes and event responses referring to it.
	MessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error)
}

// LIDStore is the part of whatsmeow's LID map the bridge reads.
type LIDStore interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
	GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error)
}

// ContactStore is the part of whatsmeow's contact store the bridge reads.
type ContactStore interface {
	GetContact(ctx context.Context, user types.JID) (types.ContactInfo, error)
}

// adapter is a *whatsmeow.Client with the device store's fields as methods.
type adapter struct {
	*whatsmeow.Client
	// ownID mirrors Store.ID. whatsmeow writes Store.ID on its own goroutines
	// while pairing and logging out, and the API reads the own JID from HTTP
	// goroutines, so the adapter keeps its own copy, set at start and on
	// PairSuccess, cleared on LoggedOut and Logout.
	ownID atomic.Pointer[types.JID]
}

var _ Client = (*adapter)(nil)

// New adapts c. A nil c gives a nil Client, so a missing client is still
// recognised by comparing with nil. The adapter registers an event handler
// on c to follow pairing and logouts.
func New(c *whatsmeow.Client) Client {
	if c == nil {
		return nil
	}
	a := &adapter{Client: c}
	if c.Store != nil && c.Store.ID != nil {
		id := *c.Store.ID
		a.ownID.Store(&id)
	}
	c.AddEventHandler(a.track)
	return a
}

// track follows the own JID through pairing and logouts.
func (a *adapter) track(evt any) {
	switch v := evt.(type) {
	case *events.PairSuccess:
		id := v.ID
		a.ownID.Store(&id)
	case *events.LoggedOut:
		a.ownID.Store(nil)
	}
}

// OwnID implements Session.
func (a *adapter) OwnID() *types.JID {
	if id := a.ownID.Load(); id != nil {
		// A copy, so a caller cannot change the adapter's.
		own := *id
		return &own
	}
	return nil
}

// Logout implements Session. whatsmeow deletes the device store on success,
// so the own JID is gone too.
func (a *adapter) Logout(ctx context.Context) error {
	if err := a.Client.Logout(ctx); err != nil {
		return err
	}
	a.ownID.Store(nil)
	return nil
}

// OwnLID implements Session.
func (a *adapter) OwnLID() types.JID {
	if a.Store == nil {
		return types.EmptyJID
	}
	return a.Store.LID
}

// PushName implements Session.
func (a *adapter) PushName() string {
	if a.Store == nil {
		return ""
	}
	return a.Store.PushName
}

// LIDs implements Session.
func (a *adapter) LIDs() LIDStore {
	if a.Store == nil || a.Store.LIDs == nil {
		return nil
	}
	return a.Store.LIDs
}

// Contacts implements Session.
func (a *adapter) Contacts() ContactStore {
	if a.Store == nil || a.Store.Contacts == nil {
		return nil
	}
	return a.Store.Contacts
}

// MessageSecret implements Session.
func (a *adapter) MessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
	if a.Store == nil || a.Store.MsgSecrets == nil {
		return nil, types.EmptyJID, nil
	}
	return a.Store.MsgSecrets.GetMessageSecret(ctx, chat, sender, id)
}

// SendIQ implements Newsletters.
func (a *adapter) SendIQ(ctx context.Context, query whatsmeow.DangerousInfoQuery) (*waBinary.Node, error) {
	// whatsmeow has no public call for a raw mex query; its own newsletter call
	// still sends the outdated query id.
	return a.DangerousInternals().SendIQ(ctx, query) //nolint:staticcheck // see above
}
