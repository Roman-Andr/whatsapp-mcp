package api

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

// Service is the part of the bridge service the handlers call. It is
// declared here, at the consumer, so handler tests run against a fake;
// *bridge.Bridge implements it.
type Service interface {
	// Messages
	Send(ctx context.Context, req bridge.SendMessageRequest) bridge.SendMessageResponse
	React(ctx context.Context, req bridge.ReactRequest) bridge.SendMessageResponse
	SendPoll(ctx context.Context, req bridge.PollRequest) bridge.SendMessageResponse
	SendLocation(ctx context.Context, req bridge.LocationRequest) bridge.SendMessageResponse
	Edit(ctx context.Context, req bridge.EditRequest) bridge.SendMessageResponse
	Delete(ctx context.Context, req bridge.DeleteRequest) bridge.SendMessageResponse
	MarkRead(ctx context.Context, req bridge.ReadRequest) bridge.SendMessageResponse
	SendTyping(ctx context.Context, req bridge.TypingRequest) bridge.SendMessageResponse
	VotePoll(ctx context.Context, req bridge.VoteRequest) bridge.SendMessageResponse
	SendContact(ctx context.Context, req bridge.ContactRequest) bridge.SendMessageResponse
	Forward(ctx context.Context, req bridge.ForwardRequest) bridge.SendMessageResponse
	Star(ctx context.Context, req bridge.StarRequest) bridge.SendMessageResponse
	RequestHistory(ctx context.Context, req bridge.HistoryRequest) bridge.SendMessageResponse
	DownloadMedia(ctx context.Context, messageID, chatJID string) (*bridge.DownloadMediaResponse, error)
	CheckReadable(ctx context.Context, chatJID string) error

	// Groups and communities
	GroupInfo(ctx context.Context, req bridge.GroupInfoRequest) bridge.GroupInfoResponse
	CreateGroup(ctx context.Context, req bridge.CreateGroupRequest) bridge.GroupChangeResponse
	LeaveGroup(ctx context.Context, req bridge.GroupChatRequest) bridge.GroupChangeResponse
	UpdateParticipants(ctx context.Context, req bridge.GroupParticipantsRequest) bridge.GroupChangeResponse
	UpdateGroupSettings(ctx context.Context, req bridge.GroupSettingsRequest) bridge.GroupChangeResponse
	JoinGroup(ctx context.Context, req bridge.JoinGroupRequest) bridge.GroupChangeResponse
	GroupRequests(ctx context.Context, chatJID string) (map[string]any, int)
	UpdateGroupRequests(ctx context.Context, req bridge.GroupRequestsUpdate) bridge.GroupChangeResponse
	ResetInviteLink(ctx context.Context, chatJID string) bridge.GroupChangeResponse
	SetDisappearing(ctx context.Context, req bridge.DisappearingRequest) bridge.SendMessageResponse
	LinkCommunity(ctx context.Context, req bridge.CommunityLinkRequest) bridge.GroupChangeResponse
	SubGroups(ctx context.Context, communityJID string) (map[string]any, int)

	// Chats and labels
	ChatAction(ctx context.Context, req bridge.ChatActionRequest) bridge.SendMessageResponse
	LabelChat(ctx context.Context, req bridge.LabelChatRequest) bridge.SendMessageResponse
	EditLabel(ctx context.Context, req bridge.LabelEditRequest) bridge.SendMessageResponse

	// Contacts
	ContactInfo(ctx context.Context, req bridge.ContactInfoRequest) bridge.ContactInfo
	Blocklist(ctx context.Context) (map[string]any, int)
	SetBlocked(ctx context.Context, req bridge.BlockRequest) bridge.SendMessageResponse
	Presence(ctx context.Context, jid string) (bridge.PresenceInfo, error)

	// Channels
	Online() bool
	Channels(ctx context.Context) ([]bridge.Channel, error)
	ChannelMessages(ctx context.Context, channelJID string, count, before int) ([]bridge.ChannelMessage, error)
	FollowChannel(ctx context.Context, req bridge.ChannelFollowRequest) bridge.SendMessageResponse

	// Session
	Status() map[string]any
	Pairing() bridge.PairingInfo
	QRImage() ([]byte, error)
	Logout(ctx context.Context) bridge.SendMessageResponse
	RequestRestart()
	Reconnect() error
}
