package bridge

import (
	"fmt"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// SendMessageRequest is the body of POST /api/send.
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
	// ReplyToMessageID quotes an earlier message of the same chat.
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	// Mentions are phone numbers or JIDs to @mention. "@<number>" in the text
	// is rewritten to the form WhatsApp expects; missing ones are appended.
	Mentions []string `json:"mentions,omitempty"`
	// Sticker sends MediaPath (a .webp image) as a sticker.
	Sticker bool `json:"sticker,omitempty"`
}

// SendMessageResponse is the response of POST /api/send and the other
// state-changing routes.
type SendMessageResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	MessageID string    `json:"message_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
	// Refused marks a call refused by the send allowlist or the media roots
	// (HTTP 403).
	Refused bool `json:"-"`
}

func failed(format string, args ...any) SendMessageResponse {
	return SendMessageResponse{Success: false, Message: fmt.Sprintf(format, args...)}
}

func blockedResponse(err error) SendMessageResponse {
	return SendMessageResponse{Success: false, Message: err.Error(), Refused: true}
}

// ReactRequest is the body of POST /api/react. An empty emoji removes our
// reaction.
type ReactRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// HistoryRequest is the body of POST /api/history.
type HistoryRequest struct {
	ChatJID string `json:"chat_jid"`
	Count   int    `json:"count"`
	// BeforeMessageID pages back from this message instead of the oldest
	// stored one. Anchoring on a recent message re-delivers messages that are
	// already stored, which repairs rows stored with incomplete details.
	BeforeMessageID string `json:"before_message_id,omitempty"`
}

// EditRequest is the body of POST /api/edit.
type EditRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Message   string `json:"message"`
}

// DeleteRequest is the body of POST /api/delete.
type DeleteRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
}

// PollRequest is the body of POST /api/poll.
type PollRequest struct {
	Recipient string   `json:"recipient"`
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	// SelectableCount is how many options one person may pick; 0 means any
	// number. Defaults to 1 when omitted.
	SelectableCount *int `json:"selectable_count,omitempty"`
}

// LocationRequest is the body of POST /api/location.
type LocationRequest struct {
	Recipient string  `json:"recipient"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
}

// ReadRequest is the body of POST /api/read.
type ReadRequest struct {
	ChatJID string `json:"chat_jid"`
	// MessageIDs to mark as read. Empty: the newest incoming messages of the
	// chat (up to 20), which is what opening the chat on the phone does.
	MessageIDs []string `json:"message_ids,omitempty"`
}

// TypingRequest is the body of POST /api/typing.
type TypingRequest struct {
	ChatJID string `json:"chat_jid"`
	// State is "composing" (typing), "recording" (recording a voice message)
	// or "paused" (stopped).
	State string `json:"state"`
}

// VoteRequest is the body of POST /api/poll/vote.
type VoteRequest struct {
	ChatJID   string   `json:"chat_jid"`
	MessageID string   `json:"message_id"` // the poll
	Options   []string `json:"options"`    // option names; empty withdraws the vote
}

// ContactRequest is the body of POST /api/contact.
type ContactRequest struct {
	Recipient string `json:"recipient"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`           // with country code
	VCard     string `json:"vcard,omitempty"` // a full vCard instead of name and phone
}

// ForwardRequest is the body of POST /api/forward.
type ForwardRequest struct {
	ChatJID   string `json:"chat_jid"` // where the message is
	MessageID string `json:"message_id"`
	Recipient string `json:"recipient"`
}

// StarRequest is the body of POST /api/star.
type StarRequest struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Starred   bool   `json:"starred"`
}

// DownloadMediaRequest represents the request body for the download media API
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse represents the response for the download media API
type DownloadMediaResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Filename  string `json:"filename,omitempty"`
	Path      string `json:"path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Mimetype  string `json:"mimetype,omitempty"`
}

// GroupInfoRequest is the body of POST /api/group.
type GroupInfoRequest struct {
	ChatJID string `json:"chat_jid"`
	// InviteLink also fetches the invite link, which WhatsApp only gives to
	// group admins. The existing link is read; it is never reset.
	InviteLink bool `json:"invite_link,omitempty"`
}

// GroupMember is one participant of a group.
type GroupMember struct {
	JID          string `json:"jid"`
	PhoneNumber  string `json:"phone_number,omitempty"`
	LID          string `json:"lid,omitempty"`
	Name         string `json:"name,omitempty"`
	IsAdmin      bool   `json:"is_admin"`
	IsSuperAdmin bool   `json:"is_super_admin"` // the group's creator
	IsMe         bool   `json:"is_me,omitempty"`
}

// GroupInfoResponse is the answer of POST /api/group.
type GroupInfoResponse struct {
	Success          bool          `json:"success"`
	Message          string        `json:"message,omitempty"`
	JID              string        `json:"jid,omitempty"`
	Name             string        `json:"name,omitempty"`
	Description      string        `json:"description,omitempty"`
	DescriptionSetBy string        `json:"description_set_by,omitempty"`
	DescriptionSetAt time.Time     `json:"description_set_at,omitzero"`
	Created          time.Time     `json:"created,omitzero"`
	Owner            string        `json:"owner,omitempty"`
	AnnounceOnly     bool          `json:"announce_only"` // only admins may send
	Locked           bool          `json:"locked"`        // only admins may edit the group info
	AddressingMode   string        `json:"addressing_mode,omitempty"`
	MemberCount      int           `json:"member_count"`
	Members          []GroupMember `json:"members,omitempty"`
	InviteLink       string        `json:"invite_link,omitempty"`
	InviteLinkError  string        `json:"invite_link_error,omitempty"`
}

// CreateGroupRequest is the body of POST /api/group/create.
type CreateGroupRequest struct {
	Name         string   `json:"name"`
	Participants []string `json:"participants"` // phone numbers or JIDs
	// Community creates a community (whose announcement group WhatsApp
	// creates with it) instead of a group.
	Community bool `json:"community,omitempty"`
}

// GroupParticipantsRequest is the body of POST /api/group/participants.
type GroupParticipantsRequest struct {
	ChatJID      string   `json:"chat_jid"`
	Action       string   `json:"action"` // add, remove, promote or demote
	Participants []string `json:"participants"`
}

// GroupSettingsRequest is the body of POST /api/group/settings. Fields left
// out are not changed.
type GroupSettingsRequest struct {
	ChatJID      string  `json:"chat_jid"`
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	AnnounceOnly *bool   `json:"announce_only,omitempty"` // only admins may send
	Locked       *bool   `json:"locked,omitempty"`        // only admins may edit the group info
	JoinApproval *bool   `json:"join_approval,omitempty"` // new members need an admin's approval
}

// GroupChatRequest names a group: POST /api/group/leave.
type GroupChatRequest struct {
	ChatJID string `json:"chat_jid"`
}

// JoinGroupRequest is the body of POST /api/group/join.
type JoinGroupRequest struct {
	Invite string `json:"invite"` // https://chat.whatsapp.com/<code> or the code
}

// ParticipantResult is the outcome for one member of a participants change.
type ParticipantResult struct {
	JID     string `json:"jid"`
	Name    string `json:"name,omitempty"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// GroupChangeResponse answers the group management routes.
type GroupChangeResponse struct {
	Success      bool                `json:"success"`
	Message      string              `json:"message"`
	Group        *GroupInfoResponse  `json:"group,omitempty"`
	Participants []ParticipantResult `json:"participants,omitempty"`
	// Refused marks a call refused by the send allowlist (HTTP 403).
	Refused bool `json:"-"`
}

func groupFailed(format string, args ...any) GroupChangeResponse {
	return GroupChangeResponse{Message: fmt.Sprintf(format, args...)}
}

func groupBlocked(err error) GroupChangeResponse {
	return GroupChangeResponse{Message: err.Error(), Refused: true}
}

// GroupRequestsUpdate is the body of POST /api/group/requests/update.
type GroupRequestsUpdate struct {
	ChatJID      string   `json:"chat_jid"`
	Action       string   `json:"action"` // approve or reject
	Participants []string `json:"participants"`
}

// DisappearingRequest is the body of POST /api/chat/disappearing.
type DisappearingRequest struct {
	ChatJID string `json:"chat_jid"`
	Timer   string `json:"timer"` // off, 24h, 7d or 90d
}

// CommunityLinkRequest is the body of POST /api/community/link.
type CommunityLinkRequest struct {
	CommunityJID string `json:"community_jid"`
	GroupJID     string `json:"group_jid"`
	Unlink       bool   `json:"unlink,omitempty"`
}

// JoinRequest is a pending request to join a group.
type JoinRequest struct {
	JID         string    `json:"jid"`
	Name        string    `json:"name,omitempty"`
	RequestedAt time.Time `json:"requested_at,omitzero"`
}

// SubGroup is a group of a community.
type SubGroup struct {
	JID       string `json:"jid"`
	Name      string `json:"name,omitempty"`
	IsDefault bool   `json:"is_announcement_group,omitempty"`
}

// ChatActionRequest is the body of POST /api/chat.
type ChatActionRequest struct {
	ChatJID string `json:"chat_jid"`
	// Action is archive, unarchive, pin, unpin, mute, unmute or delete.
	Action string `json:"action"`
	// MuteHours for "mute": how long; 0 or absent mutes forever.
	MuteHours float64 `json:"mute_hours,omitempty"`
}

// LabelChatRequest is the body of POST /api/label/chat.
type LabelChatRequest struct {
	ChatJID string `json:"chat_jid"`
	Label   string `json:"label"` // label id or name
	Labeled bool   `json:"labeled"`
}

// LabelEditRequest is the body of POST /api/label/edit.
type LabelEditRequest struct {
	// LabelID of an existing label; empty creates a new one.
	LabelID string `json:"label_id,omitempty"`
	Name    string `json:"name"`
	Color   int32  `json:"color,omitempty"` // WhatsApp's palette index, 0-19
	Deleted bool   `json:"deleted,omitempty"`
}

// ContactInfoRequest is the body of POST /api/contact/info.
type ContactInfoRequest struct {
	JID     string `json:"jid"` // phone number or JID; a group JID for its picture
	Picture bool   `json:"picture,omitempty"`
}

// ContactInfo is the answer of POST /api/contact/info.
type ContactInfo struct {
	Success      bool   `json:"success"`
	Message      string `json:"message,omitempty"`
	JID          string `json:"jid,omitempty"`
	Name         string `json:"name,omitempty"`
	OnWhatsApp   *bool  `json:"on_whatsapp,omitempty"`
	About        string `json:"about,omitempty"`
	BusinessName string `json:"business_name,omitempty"` // verified business name
	Blocked      bool   `json:"blocked"`
	PicturePath  string `json:"picture_path,omitempty"`
	PictureError string `json:"picture_error,omitempty"`
}

// BlockRequest is the body of POST /api/block.
type BlockRequest struct {
	JID     string `json:"jid"`
	Blocked bool   `json:"blocked"`
}

// PresenceInfo is what /api/presence reports.
type PresenceInfo struct {
	JID      string    `json:"jid"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	Updated  time.Time `json:"updated,omitzero"`
	Known    bool      `json:"known"`
}

// Channel describes a WhatsApp channel (newsletter).
type Channel struct {
	JID         string `json:"jid"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Subscribers int    `json:"subscribers"`
	Role        string `json:"role,omitempty"` // subscriber, admin, owner, guest
	Muted       bool   `json:"muted"`
	InviteLink  string `json:"invite_link,omitempty"`
	Verified    bool   `json:"verified"`
}

// ChannelMessage is a post of a channel.
type ChannelMessage struct {
	ServerID  int            `json:"server_id"`
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Text      string         `json:"text,omitempty"`
	MediaType string         `json:"media_type,omitempty"`
	Views     int            `json:"views"`
	Forwards  int            `json:"forwards"`
	Reactions map[string]int `json:"reactions,omitempty"`
}

// ChannelFollowRequest is the body of POST /api/channel/follow.
type ChannelFollowRequest struct {
	Channel string `json:"channel"` // channel JID, invite link or invite code
	Follow  bool   `json:"follow"`
}

// PairingInfo is the answer of GET /api/qr.
type PairingInfo = wa.PairingInfo

// SessionStatus is the session state in GET /api/status.
type SessionStatus = wa.SessionStatus

// Summary is how the audit log shows a location.
func (r LocationRequest) Summary() string {
	return content.FormatLocation(r.Latitude, r.Longitude, r.Name, r.Address)
}
