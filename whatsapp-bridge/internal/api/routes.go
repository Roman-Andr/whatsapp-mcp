package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// noGuard marks a route that the guard does not wrap: reads, and the two
// routes that have never been guarded (/api/history and
// /api/connection/reconnect, which has its own throttle).
const noGuard policy.Kind = -1

// Per-route timeouts on calls to WhatsApp. Routes without one run on the
// request context alone.
const (
	lookupTimeout      = 30 * time.Second
	groupChangeTimeout = 60 * time.Second
	downloadTimeout    = 2 * time.Minute
)

// Audit endpoint names, shared by the route table and the audit entries.
const (
	epSend             = "send"
	epReact            = "react"
	epPoll             = "poll"
	epLocation         = "location"
	epEdit             = "edit"
	epDelete           = "delete"
	epRead             = "read"
	epTyping           = "typing"
	epLogout           = "logout"
	epReconnect        = "reconnect"
	epGroupCreate      = "group_create"
	epGroupLeave       = "group_leave"
	epGroupParticipant = "group_participants"
	epGroupSettings    = "group_settings"
	epGroupJoin        = "group_join"
	epChat             = "chat"
	epLabelChat        = "label_chat"
	epLabelEdit        = "label_edit"
	epBlock            = "block"
	epChannelFollow    = "channel_follow"
	epGroupRequests    = "group_requests"
	epGroupInviteReset = "group_invite_reset"
	epDisappearing     = "disappearing"
	epCommunityLink    = "community_link"
	epVote             = "vote"
	epContact          = "contact"
	epSticker          = "sticker"
	epForward          = "forward"
	epStar             = "star"
)

// route is one REST route.
type route struct {
	path     string      // path-only ServeMux pattern: the handlers check the method, so 405 stays plain text without an Allow header
	endpoint string      // audit endpoint name ("" for routes that are not audited)
	kind     policy.Kind // what the guard checks, or noGuard
	handle   http.HandlerFunc
}

// handler holds what the route handlers share.
type handler struct {
	Deps
	log *slog.Logger
	// quit is closed when the server shuts down, which ends the event
	// streams; http.Server.Shutdown would otherwise wait for them. It is
	// nil (never closed) for a bare NewHandler.
	quit chan struct{}
}

func newHandler(d Deps) *handler {
	log := d.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &handler{Deps: d, log: log}
}

// routes is the route table, in registration order.
func (h *handler) routes() []route {
	return []route{
		// messages.go
		{"/api/send", epSend, policy.ReachesOthers, h.send},
		{"/api/react", epReact, policy.ReachesOthers, h.react},
		{"/api/poll", epPoll, policy.ReachesOthers, h.poll},
		{"/api/location", epLocation, policy.ReachesOthers, h.location},
		{"/api/edit", epEdit, policy.ReachesOthers, h.edit},
		{"/api/delete", epDelete, policy.ReachesOthers, h.delete},
		{"/api/read", epRead, policy.SignalOnly, h.markRead},
		{"/api/typing", epTyping, policy.SignalOnly, h.typing},
		{"/api/history", "", noGuard, h.history},
		{"/api/download", "", noGuard, h.download},
		{"/api/poll/vote", epVote, policy.ReachesOthers, h.vote},
		{"/api/contact", epContact, policy.ReachesOthers, h.sendContact},
		{"/api/sticker", epSticker, policy.ReachesOthers, h.sticker},
		{"/api/forward", epForward, policy.ReachesOthers, h.forward},
		{"/api/star", epStar, policy.OwnAccount, h.star},
		// session.go
		{"/api/status", "", noGuard, h.status},
		{"/api/qr", "", noGuard, h.qr},
		{"/api/logout", epLogout, policy.OwnAccount, h.logout},
		{"/api/connection/reconnect", epReconnect, noGuard, h.reconnect},
		// events.go
		{"/api/events", "", noGuard, h.events},
		// groups.go
		{"/api/group", "", noGuard, h.groupInfo},
		{"/api/group/create", epGroupCreate, policy.ReachesOthers, h.createGroup},
		{"/api/group/leave", epGroupLeave, policy.ReachesOthers, h.leaveGroup},
		{"/api/group/participants", epGroupParticipant, policy.ReachesOthers, h.groupParticipants},
		{"/api/group/settings", epGroupSettings, policy.ReachesOthers, h.groupSettings},
		{"/api/group/join", epGroupJoin, policy.ReachesOthers, h.joinGroup},
		{"/api/group/requests", "", noGuard, h.groupRequests},
		{"/api/group/requests/update", epGroupRequests, policy.ReachesOthers, h.updateGroupRequests},
		{"/api/group/invite/reset", epGroupInviteReset, policy.ReachesOthers, h.resetInvite},
		{"/api/chat/disappearing", epDisappearing, policy.ReachesOthers, h.disappearing},
		{"/api/community/link", epCommunityLink, policy.ReachesOthers, h.linkCommunity},
		{"/api/community/groups", "", noGuard, h.communityGroups},
		// chats.go
		{"/api/chat", epChat, policy.OwnAccount, h.chatAction},
		{"/api/label/chat", epLabelChat, policy.OwnAccount, h.labelChat},
		{"/api/label/edit", epLabelEdit, policy.OwnAccount, h.editLabel},
		// contacts.go
		{"/api/contact/info", "", noGuard, h.contactInfo},
		{"/api/blocklist", "", noGuard, h.blocklist},
		{"/api/block", epBlock, policy.OwnAccount, h.block},
		{"/api/presence", "", noGuard, h.presence},
		// channels.go
		{"/api/channels", "", noGuard, h.channels},
		{"/api/channel/messages", "", noGuard, h.channelMessages},
		{"/api/channel/follow", epChannelFollow, policy.OwnAccount, h.followChannel},
	}
}

// NewHandler builds the REST API. Every route requires the token, which is
// checked before routing.
func NewHandler(d Deps) http.Handler { return newHandler(d).serve() }

// serve registers the route table on a mux, each route behind its guard and
// all of them behind the token.
func (h *handler) serve() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range h.routes() {
		handle := rt.handle
		if rt.kind != noGuard {
			handle = h.guard(rt.endpoint, rt.kind, handle)
		}
		mux.HandleFunc(rt.path, handle)
	}
	return requireToken(h.Token, mux)
}
