// table.go is the source of truth for the tool list, maintained by hand
// (no generator). Each entry names its handler, which lives in the file of
// its area (messages.go, chats.go, send.go, ...).

package tools

// toolTable lists every tool in the order the server registers them.
var toolTable = []toolSpec{
	{
		name: "search_contacts", ann: annRead, out: outContacts,
		handle: searchContacts,
		params: []param{
			{name: "query", typ: "string", required: true},
			{name: "limit", typ: "integer", def: 50},
		},
		doc: `Search WhatsApp contacts by name or phone number, including saved contacts without a chat.

    Args:
        query: Search term to match against contact names or phone numbers
        limit: Maximum number of contacts to return (default 50, max 500)
    `,
	},
	{
		name: "list_messages", ann: annRead, out: outText,
		handle: listMessages,
		params: []param{
			{name: "after", typ: "string", nullable: true},
			{name: "before", typ: "string", nullable: true},
			{name: "sender_phone_number", typ: "string", nullable: true},
			{name: "chat_jid", typ: "string", nullable: true},
			{name: "query", typ: "string", nullable: true},
			{name: "limit", typ: "integer", def: 20},
			{name: "page", typ: "integer", def: 0},
			{name: "include_context", typ: "boolean", def: true},
			{name: "context_before", typ: "integer", def: 1},
			{name: "context_after", typ: "integer", def: 1},
			{name: "starred_only", typ: "boolean", def: false},
		},
		doc: `Get WhatsApp messages matching specified criteria with optional context.

    Each line shows the message id and chat JID (needed by send_reaction, send_message's
    reply_to_message_id, download_media and get_message_context), what it replies to,
    reactions, poll votes, voice transcripts and whether it was edited or deleted.

    Args:
        after: Optional ISO-8601 formatted string to only return messages after this date
        before: Optional ISO-8601 formatted string to only return messages before this date
        sender_phone_number: Optional phone number or sender JID to filter messages by sender
        chat_jid: Optional chat JID to filter messages by chat
        query: Optional search term; matches whole words (full-text) and substrings
        limit: Maximum number of messages to return (default 20, max 500)
        page: Page number for pagination (default 0)
        include_context: Whether to include messages before and after matches (default True)
        context_before: Number of messages to include before each match (default 1)
        context_after: Number of messages to include after each match (default 1)
        starred_only: Only messages starred on any of your devices
    `,
	},
	{
		name: "list_chats", ann: annRead, out: outChats,
		handle: listChats,
		params: []param{
			{name: "query", typ: "string", nullable: true},
			{name: "limit", typ: "integer", def: 20},
			{name: "page", typ: "integer", def: 0},
			{name: "include_last_message", typ: "boolean", def: true},
			{name: "sort_by", typ: "string", def: "last_active"},
			{name: "label", typ: "string", nullable: true},
		},
		doc: `Get WhatsApp chats matching specified criteria.

    Each chat has its unread count, whether it is archived or pinned, until when it is muted
    and its labels. Chats deleted on a device are left out.

    Args:
        query: Optional search term to filter chats by name or JID
        limit: Maximum number of chats to return (default 20, max 500)
        page: Page number for pagination (default 0)
        include_last_message: Whether to include the last message in each chat (default True)
        sort_by: Field to sort results by, either "last_active" or "name" (default "last_active")
        label: Optional label name or id (WhatsApp Business) to list only chats carrying it
    `,
	},
	{
		name: "list_unread_chats", ann: annRead, out: outChats,
		handle: listUnreadChats,
		params: []param{
			{name: "limit", typ: "integer", def: 20},
		},
		doc: `List WhatsApp chats with unread messages, most recently active first.

    ` + "`" + `unread_count` + "`" + ` is how many messages others sent since the chat was last read, here
    (mark_read) or on the phone. Read them with list_messages(chat_jid=...), then mark_read.

    Args:
        limit: Maximum number of chats to return (default 20, max 500)
    `,
	},
	{
		name: "get_chat", ann: annRead, out: outOptChat,
		handle: getChat,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "include_last_message", typ: "boolean", def: true},
		},
		doc: `Get WhatsApp chat metadata by JID.

    Args:
        chat_jid: The JID of the chat to retrieve
        include_last_message: Whether to include the last message (default True)
    `,
	},
	{
		name: "get_direct_chat_by_contact", ann: annRead, out: outOptChat,
		handle: getDirectChatByContact,
		params: []param{
			{name: "sender_phone_number", typ: "string", required: true},
		},
		doc: `Get WhatsApp chat metadata by sender phone number (also finds chats addressed by LID).

    Args:
        sender_phone_number: The phone number to search for
    `,
	},
	{
		name: "get_contact_chats", ann: annRead, out: outChats,
		handle: getContactChats,
		params: []param{
			{name: "jid", typ: "string", required: true},
			{name: "limit", typ: "integer", def: 20},
			{name: "page", typ: "integer", def: 0},
		},
		doc: `Get all WhatsApp chats involving the contact.

    Args:
        jid: The contact's JID to search for
        limit: Maximum number of chats to return (default 20, max 500)
        page: Page number for pagination (default 0)
    `,
	},
	{
		name: "get_last_interaction", ann: annRead, out: outOptText,
		handle: getLastInteraction,
		params: []param{
			{name: "jid", typ: "string", required: true},
		},
		doc: `Get most recent WhatsApp message involving the contact.

    Args:
        jid: The JID of the contact to search for
    `,
	},
	{
		name: "get_message_context", ann: annRead, out: outText,
		handle: getMessageContext,
		params: []param{
			{name: "message_id", typ: "string", required: true},
			{name: "before", typ: "integer", def: 5},
			{name: "after", typ: "integer", def: 5},
			{name: "chat_jid", typ: "string", nullable: true},
		},
		doc: `Get context around a specific WhatsApp message, oldest first.

    Args:
        message_id: The ID of the message to get context for
        before: Number of messages to include before the target message (default 5)
        after: Number of messages to include after the target message (default 5)
        chat_jid: Optional chat JID of the message
    `,
	},
	{
		name: "send_message", ann: annSend, out: outSendResult,
		handle: sendMessage,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "message", typ: "string", required: true},
			{name: "reply_to_message_id", typ: "string", nullable: true},
			{name: "mentions", typ: "array", nullable: true},
		},
		doc: `Send a WhatsApp message to a person or group. For group chats use the JID.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        message: The message text to send
        reply_to_message_id: Optional id of a message in the same chat to quote (reply to)
        mentions: Optional phone numbers or JIDs to @mention; write "@<number>" in the text
                  where the mention should appear (missing ones are appended)

    Returns:
        A dictionary containing success status, a status message and the sent message id
    `,
	},
	{
		name: "send_file", ann: annSend, out: outSendResult,
		handle: sendFile,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "media_path", typ: "string", required: true},
			{name: "caption", typ: "string", def: ""},
			{name: "reply_to_message_id", typ: "string", nullable: true},
		},
		doc: `Send a file such as a picture, raw audio, video or document via WhatsApp to the specified recipient. For group messages use the JID.

    Documents (PDF, Word, Excel, ...) are sent with their file name and MIME type.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the media file to send (image, video, document). It must be inside the bridge's media roots: by default downloaded media, ~/.local/share/whatsapp-mcp/outbox or the temp directory
        caption: Optional text shown under the file
        reply_to_message_id: Optional id of a message in the same chat to quote (reply to)

    Returns:
        A dictionary containing success status, a status message and the sent message id
    `,
	},
	{
		name: "send_audio_message", ann: annSend, out: outSendResult,
		handle: sendAudioMessage,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "media_path", typ: "string", required: true},
		},
		doc: `Send any audio file as a WhatsApp voice message to the specified recipient. For group messages use the JID.
    Non-.ogg files are converted to Opus (ffmpeg if installed, otherwise PyAV). If conversion fails, use send_file instead.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the audio file to send (will be converted to Opus .ogg if it's not a .ogg file). It must be inside the bridge's media roots: by default downloaded media, ~/.local/share/whatsapp-mcp/outbox or the temp directory

    Returns:
        A dictionary containing success status, a status message and the sent message id
    `,
	},
	{
		name: "send_reaction", ann: annSend, out: outResult,
		handle: sendReaction,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
			{name: "emoji", typ: "string", required: true},
		},
		doc: `React to a WhatsApp message with an emoji, or remove your reaction.

    Args:
        chat_jid: The JID of the chat containing the message
        message_id: The id of the message to react to (shown by list_messages)
        emoji: The emoji to react with, e.g. "👍"; an empty string removes your reaction

    Returns:
        A dictionary containing success status and a status message
    `,
	},
	{
		name: "send_poll", ann: annSend, out: outSendResult,
		handle: sendPoll,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "question", typ: "string", required: true},
			{name: "options", typ: "array", required: true},
			{name: "selectable_count", typ: "integer", def: 1},
		},
		doc: `Send a poll. Votes are shown next to the poll by list_messages as they arrive.

    Args:
        recipient: Phone number or JID of the person or group
        question: The poll question
        options: 2 to 12 distinct answer options
        selectable_count: How many options one person may pick (default 1; 0 = any number)
    `,
	},
	{
		name: "send_location", ann: annSend, out: outSendResult,
		handle: sendLocation,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "latitude", typ: "number", required: true},
			{name: "longitude", typ: "number", required: true},
			{name: "name", typ: "string", def: ""},
			{name: "address", typ: "string", def: ""},
		},
		doc: `Send a location pin.

    Args:
        recipient: Phone number or JID of the person or group
        latitude: Latitude in degrees (-90 to 90)
        longitude: Longitude in degrees (-180 to 180)
        name: Optional name of the place
        address: Optional address shown under the name
    `,
	},
	{
		name: "edit_message", ann: annSend, out: outSendResult,
		handle: editMessage,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
			{name: "new_text", typ: "string", required: true},
		},
		doc: `Change the text of a message you sent. WhatsApp allows this for 20 minutes after sending,
    for text messages only. Recipients see the message marked as edited.

    Args:
        chat_jid: The JID of the chat containing the message
        message_id: The id of your message (shown by list_messages)
        new_text: The replacement text
    `,
	},
	{
		name: "delete_message", ann: annSend, out: outSendResult,
		handle: deleteMessage,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
		},
		doc: `Delete a message for everyone in the chat. Recipients see "This message was deleted".
    Your own messages can always be deleted; in a group where you are an admin, anyone's.
    The local copy keeps its text, marked deleted.

    Args:
        chat_jid: The JID of the chat containing the message
        message_id: The id of your message (shown by list_messages)
    `,
	},
	{
		name: "vote_poll", ann: annSend, out: outSendResult,
		handle: votePoll,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
			{name: "options", typ: "array", required: true},
		},
		doc: `Vote in a WhatsApp poll, change your vote, or withdraw it with an empty list.

    Args:
        chat_jid: The JID of the chat containing the poll
        message_id: The poll's message id (polls show as "[poll] question: a / b" in list_messages)
        options: The option names to choose, exactly as in the poll; [] withdraws your vote
    `,
	},
	{
		name: "send_contact", ann: annSend, out: outSendResult,
		handle: sendContact,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "name", typ: "string", required: true},
			{name: "phone", typ: "string", def: ""},
			{name: "vcard", typ: "string", def: ""},
		},
		doc: `Send a contact card that the recipient can save or message.

    Args:
        recipient: Phone number or JID of the person or group
        name: Name shown on the card
        phone: The contact's phone number with country code
        vcard: Optional full vCard text instead of phone (for several numbers, e-mail, ...)
    `,
	},
	{
		name: "send_sticker", ann: annSend, out: outSendResult,
		handle: sendSticker,
		params: []param{
			{name: "recipient", typ: "string", required: true},
			{name: "media_path", typ: "string", required: true},
			{name: "reply_to_message_id", typ: "string", nullable: true},
		},
		doc: `Send an image as a WhatsApp sticker. PNG, JPEG and other images are converted to a
    512x512 WebP first.

    Args:
        recipient: Phone number or JID of the person or group
        media_path: Absolute path to the image. It must be inside the bridge's media roots: by default downloaded media, ~/.local/share/whatsapp-mcp/outbox or the temp directory
        reply_to_message_id: Optional id of a message in the same chat to reply to
    `,
	},
	{
		name: "forward_message", ann: annSend, out: outSendResult,
		handle: forwardMessage,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
			{name: "recipient", typ: "string", required: true},
		},
		doc: `Forward a message (text or media) to another chat; it arrives marked "Forwarded".

    Args:
        chat_jid: The JID of the chat the message is in
        message_id: The id of the message to forward
        recipient: Phone number or JID to forward it to
    `,
	},
	{
		name: "star_message", ann: annOwn, out: outResult,
		handle: starMessage,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_id", typ: "string", required: true},
			{name: "starred", typ: "boolean", def: true},
		},
		doc: `Star or unstar a message on every device of the account. Nobody else sees it.
    list_messages(starred_only=True) lists starred messages.

    Args:
        chat_jid: The JID of the chat containing the message
        message_id: The id of the message
        starred: True stars, False unstars
    `,
	},
	{
		name: "mark_read", ann: annSignal, out: outResult,
		handle: markRead,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "message_ids", typ: "array", nullable: true},
		},
		doc: `Send read receipts (blue ticks) for messages in a chat.

    Args:
        chat_jid: The JID of the chat
        message_ids: Optional ids of the messages to mark; by default the newest incoming
                     messages of the chat (up to 20)
    `,
	},
	{
		name: "send_typing", ann: annSignal, out: outResult,
		handle: sendTyping,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "state", typ: "string", def: "composing"},
		},
		doc: `Show or clear the "typing…" indicator in a chat. WhatsApp clears it by itself after
    a while; send "paused" to clear it at once.

    Args:
        chat_jid: The JID of the chat (or a phone number)
        state: "composing" (typing), "recording" (recording a voice message) or "paused"
    `,
	},
	{
		name: "get_group_info", ann: annReadRemote, out: outDict,
		handle: getGroupInfo,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "include_invite_link", typ: "boolean", def: true},
		},
		doc: `Get a WhatsApp group's details from WhatsApp: name, description, creation date, owner,
    whether only admins may send, and every member with name, phone number, LID and admin role
    (admins listed first).

    Args:
        chat_jid: The group JID (ending in @g.us)
        include_invite_link: Also return the invite link; WhatsApp only gives it to admins
                             (the existing link is read, never reset)
    `,
	},
	{
		name: "manage_chat", ann: annOwn, out: outResult,
		handle: manageChat,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "action", typ: "string", required: true},
			{name: "mute_hours", typ: "number", nullable: true},
		},
		doc: `Archive, pin, mute or delete a chat in your chat list, on every device of the account.
    Nobody else is notified.

    "delete" removes the chat from WhatsApp on all devices; the bridge keeps its copy of the
    messages (still readable here) and the chat reappears when a new message arrives.

    Args:
        chat_jid: The JID of the chat
        action: "archive", "unarchive", "pin", "unpin", "mute", "unmute" or "delete"
        mute_hours: For "mute": how many hours; omit or 0 to mute forever
    `,
	},
	{
		name: "list_labels", ann: annRead, out: outDictList,
		handle: listLabels,
		params: []param{},
		doc: `List the WhatsApp Business labels of the account (id, name, colour, number of chats).

    Personal accounts have none. Use list_chats(label=...) to see the chats carrying one.
    `,
	},
	{
		name: "label_chat", ann: annOwn, out: outResult,
		handle: labelChat,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "label", typ: "string", required: true},
			{name: "labeled", typ: "boolean", def: true},
		},
		doc: `Add a WhatsApp Business label to a chat, or remove it.

    Args:
        chat_jid: The JID of the chat
        label: The label's name or id (see list_labels)
        labeled: True adds the label, False removes it
    `,
	},
	{
		name: "edit_label", ann: annOwn, out: outSendResult,
		handle: editLabel,
		params: []param{
			{name: "name", typ: "string", required: true},
			{name: "label_id", typ: "string", nullable: true},
			{name: "color", typ: "integer", def: 0},
			{name: "deleted", typ: "boolean", def: false},
		},
		doc: `Create, rename, recolour or delete a WhatsApp Business label.

    Args:
        name: The label's name
        label_id: Id of the label to change; omit to create a new label
        color: Colour index 0-19 of WhatsApp's palette
        deleted: True deletes the label (label_id required)

    Returns:
        success, a status message and, as message_id, the label's id
    `,
	},
	{
		name: "create_group", ann: annSend, out: outDict,
		handle: createGroup,
		params: []param{
			{name: "name", typ: "string", required: true},
			{name: "participants", typ: "array", required: true},
			{name: "community", typ: "boolean", def: false},
		},
		doc: `Create a WhatsApp group (or a community) with you as admin. Everyone added gets a notification.

    People whose privacy settings forbid being added are reported per participant; WhatsApp
    sends them an invitation instead.

    Args:
        name: Group name, at most 25 characters
        participants: Phone numbers (with country code) or JIDs of the members to add
                      (may be empty for a community)
        community: True creates a community; link groups to it with link_community_group
    `,
	},
	{
		name: "leave_group", ann: annSend, out: outDict,
		handle: leaveGroup,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
		},
		doc: `Leave a WhatsApp group. The other members see that you left.

    Args:
        chat_jid: The group JID (ending in @g.us)
    `,
	},
	{
		name: "update_group_participants", ann: annSend, out: outDict,
		handle: updateGroupParticipants,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "action", typ: "string", required: true},
			{name: "participants", typ: "array", required: true},
		},
		doc: `Add, remove, promote to admin or demote members of a WhatsApp group (you must be an admin).

    Args:
        chat_jid: The group JID (ending in @g.us)
        action: "add", "remove", "promote" or "demote"
        participants: Phone numbers or JIDs (as get_group_info lists them)
    `,
	},
	{
		name: "update_group_settings", ann: annSend, out: outDict,
		handle: updateGroupSettings,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "name", typ: "string", nullable: true},
			{name: "description", typ: "string", nullable: true},
			{name: "announce_only", typ: "boolean", nullable: true},
			{name: "locked", typ: "boolean", nullable: true},
			{name: "join_approval", typ: "boolean", nullable: true},
		},
		doc: `Change a WhatsApp group's name, description or permissions. Only given fields change.

    Args:
        chat_jid: The group JID (ending in @g.us)
        name: New name, at most 25 characters
        description: New description ("" clears it)
        announce_only: True: only admins may send messages
        locked: True: only admins may edit the group info
        join_approval: True: people joining through the invite link need an admin's approval
    `,
	},
	{
		name: "join_group", ann: annSend, out: outDict,
		handle: joinGroup,
		params: []param{
			{name: "invite", typ: "string", required: true},
		},
		doc: `Join a WhatsApp group through an invite link. The members see that you joined.

    Args:
        invite: The https://chat.whatsapp.com/... link or its code
    `,
	},
	{
		name: "get_group_join_requests", ann: annReadRemote, out: outDict,
		handle: getGroupJoinRequests,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
		},
		doc: `List pending requests to join a WhatsApp group (groups with join approval; admins only).

    Args:
        chat_jid: The group JID (ending in @g.us)
    `,
	},
	{
		name: "update_group_join_requests", ann: annSend, out: outDict,
		handle: updateGroupJoinRequests,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "action", typ: "string", required: true},
			{name: "participants", typ: "array", required: true},
		},
		doc: `Approve or reject requests to join a WhatsApp group.

    Args:
        chat_jid: The group JID (ending in @g.us)
        action: "approve" or "reject"
        participants: JIDs from get_group_join_requests
    `,
	},
	{
		name: "reset_group_invite_link", ann: annSend, out: outDict,
		handle: resetGroupInviteLink,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
		},
		doc: `Revoke a group's invite link and create a new one; the old link stops working.

    Args:
        chat_jid: The group JID (ending in @g.us)
    `,
	},
	{
		name: "set_disappearing_messages", ann: annSend, out: outResult,
		handle: setDisappearingMessages,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "timer", typ: "string", required: true},
		},
		doc: `Turn disappearing messages on or off for a chat or group. Everyone in it sees the change.

    Args:
        chat_jid: The JID of the chat or group
        timer: "off", "24h", "7d" or "90d"
    `,
	},
	{
		name: "link_community_group", ann: annSend, out: outDict,
		handle: linkCommunityGroup,
		params: []param{
			{name: "community_jid", typ: "string", required: true},
			{name: "group_jid", typ: "string", required: true},
			{name: "unlink", typ: "boolean", def: false},
		},
		doc: `Add a group to a WhatsApp community you administer, or remove it from the community.

    Args:
        community_jid: The community's JID
        group_jid: The group's JID
        unlink: True removes the group from the community
    `,
	},
	{
		name: "list_community_groups", ann: annReadRemote, out: outDict,
		handle: listCommunityGroups,
		params: []param{
			{name: "community_jid", typ: "string", required: true},
		},
		doc: `List the groups of a WhatsApp community, including its announcement group.

    Args:
        community_jid: The community's JID
    `,
	},
	{
		name: "request_chat_history", ann: annReadRemote, out: outResult,
		handle: requestChatHistory,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "count", typ: "integer", def: 50},
			{name: "before_message_id", typ: "string", nullable: true},
		},
		doc: `Ask the phone for messages older than the oldest one stored for a chat.

    The messages arrive asynchronously (usually within a minute, only while the phone is
    online); read them afterwards with list_messages. Repeat to page further back.

    Args:
        chat_jid: The JID of the chat
        count: How many older messages to request (default 50, max 500)
        before_message_id: Optional message to page back from instead of the oldest stored one;
                           re-fetching already stored messages repairs missing sender details
    `,
	},
	{
		name: "download_media", ann: annReadRemote, out: outDict,
		handle: downloadMedia,
		params: []param{
			{name: "message_id", typ: "string", required: true},
			{name: "chat_jid", typ: "string", required: true},
		},
		doc: `Download media from a WhatsApp message and get the local file path.

    Media that expired on WhatsApp's servers is re-requested from the phone automatically,
    which can take up to a minute.

    Args:
        message_id: The ID of the message containing the media
        chat_jid: The JID of the chat containing the message

    Returns:
        A dictionary containing success status, a status message, and the file path if successful
    `,
	},
	{
		name: "view_media", ann: annReadRemote, out: outImage,
		handle: viewMedia,
		params: []param{
			{name: "message_id", typ: "string", required: true},
			{name: "chat_jid", typ: "string", required: true},
		},
		doc: `Show the image (photo, screenshot or sticker) of a WhatsApp message, or the
    first frame of a video.

    Downloads it if needed and returns the picture itself, scaled down if large.

    Args:
        message_id: The ID of the message containing the image
        chat_jid: The JID of the chat containing the message
    `,
	},
	{
		name: "get_contact_info", ann: annReadRemote, out: outDict,
		handle: getContactInfo,
		params: []param{
			{name: "jid", typ: "string", required: true},
			{name: "include_picture", typ: "boolean", def: false},
		},
		doc: `Look a person (or group) up on WhatsApp: name, whether the number is on WhatsApp,
    their "about" text, verified business name, whether you blocked them, and optionally
    their profile picture saved to a file (picture_path).

    Args:
        jid: Phone number with country code, or JID
        include_picture: Also download the profile picture
    `,
	},
	{
		name: "view_profile_picture", ann: annReadRemote, out: outImage,
		handle: viewProfilePicture,
		params: []param{
			{name: "jid", typ: "string", required: true},
		},
		doc: `Show the profile picture of a person or group as an image.

    Args:
        jid: Phone number with country code, or JID (a group JID for the group's picture)
    `,
	},
	{
		name: "block_contact", ann: annOwn, out: outResult,
		handle: blockContact,
		params: []param{
			{name: "jid", typ: "string", required: true},
			{name: "blocked", typ: "boolean", def: true},
		},
		doc: `Block or unblock a person. Blocked people cannot message or call you.

    Args:
        jid: Phone number with country code, or JID
        blocked: True blocks, False unblocks
    `,
	},
	{
		name: "list_blocked_contacts", ann: annReadRemote, out: outDict,
		handle: listBlockedContacts,
		params: []param{},
		doc:    `List the people you blocked on WhatsApp.`,
	},
	{
		name: "get_presence", ann: annReadRemote, out: outDict,
		handle: getPresence,
		params: []param{
			{name: "jid", typ: "string", required: true},
		},
		doc: `Whether a person is online now, or when they were last seen (if they share it).

    Needs WHATSAPP_PRESENCE=1 on the bridge: WhatsApp only tells online clients, and while the
    bridge shows as online your phone gets no notifications. The first call subscribes; updates
    then also arrive as "presence" events in wait_for_events.

    Args:
        jid: Phone number with country code, or JID
    `,
	},
	{
		name: "list_statuses", ann: annRead, out: outText,
		handle: listStatuses,
		params: []param{
			{name: "hours", typ: "integer", def: 24},
			{name: "limit", typ: "integer", def: 50},
		},
		doc: `List your contacts' status updates (stories) received in the last hours, newest first.
    Image and video statuses can be opened with view_media / download_media
    (chat_jid "status@broadcast").

    Args:
        hours: How far back to look (default 24; statuses expire after 24 hours)
        limit: Maximum number of statuses (default 50)
    `,
	},
	{
		name: "post_status", ann: annSend, out: outSendResult,
		handle: postStatus,
		params: []param{
			{name: "text", typ: "string", def: ""},
			{name: "media_path", typ: "string", def: ""},
			{name: "caption", typ: "string", def: ""},
		},
		doc: `Post a WhatsApp status (story) for 24 hours, seen by the contacts your status privacy
    settings allow.

    Args:
        text: Text of a text status
        media_path: Absolute path of an image or video for a media status (instead of text). It must be inside the bridge's media roots: by default downloaded media, ~/.local/share/whatsapp-mcp/outbox or the temp directory
        caption: Caption of a media status
    `,
	},
	{
		name: "list_channels", ann: annReadRemote, out: outDict,
		handle: listChannels,
		params: []param{},
		doc:    `List the WhatsApp channels you follow or run: name, description, followers, your role.`,
	},
	{
		name: "get_channel_messages", ann: annReadRemote, out: outDict,
		handle: getChannelMessages,
		params: []param{
			{name: "channel_jid", typ: "string", required: true},
			{name: "count", typ: "integer", def: 20},
			{name: "before_server_id", typ: "integer", def: 0},
		},
		doc: `Read the latest posts of a WhatsApp channel, with views, forwards and reaction counts.

    Args:
        channel_jid: The channel's JID (…@newsletter, from list_channels)
        count: How many posts (default 20, max 100)
        before_server_id: Page back from this post's server_id
    `,
	},
	{
		name: "follow_channel", ann: annOwn, out: outResult,
		handle: followChannel,
		params: []param{
			{name: "channel", typ: "string", required: true},
			{name: "follow", typ: "boolean", def: true},
		},
		doc: `Follow or unfollow a WhatsApp channel.

    Args:
        channel: The channel's JID, its whatsapp.com/channel/… link or the link's code
        follow: True follows, False unfollows
    `,
	},
	{
		name: "transcribe_voice_message", ann: annReadRemote, out: outDict,
		handle: transcribeVoiceMessage,
		params: []param{
			{name: "message_id", typ: "string", required: true},
			{name: "chat_jid", typ: "string", required: true},
			{name: "language", typ: "string", nullable: true},
		},
		doc: `Transcribe a WhatsApp voice or audio message locally (faster-whisper, no cloud).

    The first call downloads the speech model and is slow; transcripts are cached and
    shown by list_messages afterwards.

    Args:
        message_id: The ID of the audio message
        chat_jid: The JID of the chat containing the message
        language: Optional language code such as "en" or "ru"; detected automatically if omitted
    `,
	},
	{
		name: "wait_for_events", ann: annRead, out: outDict,
		handle: waitForEvents,
		params: []param{
			{name: "timeout_seconds", typ: "integer", def: 60},
			{name: "chat_jid", typ: "string", nullable: true},
			{name: "max_events", typ: "integer", def: 20},
		},
		doc: `Wait for new WhatsApp activity (messages, edits, deletions, reactions, group membership
    changes, calls) and return it.

    A "session" event means the WhatsApp session changed state (for example logged_out);
    it is delivered even when filtering by chat_jid.

    Returns as soon as at least one event arrived and no more followed for two seconds,
    or when the timeout passes.

    Args:
        timeout_seconds: How long to wait at most (default 60, max 600)
        chat_jid: Optional chat JID to only watch one chat
        max_events: Stop after this many events (default 20)
    `,
	},
	{
		name: "export_chat", ann: annReadRemote, out: outDict,
		handle: exportChat,
		params: []param{
			{name: "chat_jid", typ: "string", required: true},
			{name: "format", typ: "string", def: "txt"},
			{name: "after", typ: "string", nullable: true},
			{name: "before", typ: "string", nullable: true},
			{name: "include_media", typ: "boolean", def: false},
		},
		doc: `Export a chat's history to a file under store/exports/ and return its path.

    Args:
        chat_jid: The JID of the chat
        format: "txt" (readable lines), "json" (every field) or "html" (a page like the app)
        after: Optional ISO-8601 time; only messages after it
        before: Optional ISO-8601 time; only messages before it
        include_media: Also download every photo, voice message and document and pack
                       everything into a ZIP archive (slow for large chats)
    `,
	},
	{
		name: "list_accounts", ann: annReadRemote, out: outDictList,
		handle: listAccounts,
		params: []param{},
		doc: `List the WhatsApp accounts this server can use (WHATSAPP_ACCOUNTS), whether each bridge is
    connected, and which one is the default. Pass a name as ` + "`" + `account` + "`" + ` to any tool.`,
	},
	{
		name: "get_bridge_status", ann: annRead, out: outDict,
		handle: getBridgeStatus,
		params: []param{},
		doc: `Check whether the WhatsApp bridge is running and connected.

    ` + "`" + `session.state` + "`" + ` is connected, disconnected (reconnecting), qr_required, logged_out,
    stream_replaced, connect_failure, temporary_ban or client_outdated. When
    ` + "`" + `session.needs_action` + "`" + ` is true a ` + "`" + `warning` + "`" + ` says what the owner has to do; tell them.
    ` + "`" + `send_allowlist` + "`" + ` lists the only chats sending is allowed to (empty: unrestricted).
    ` + "`" + `read_allowlist` + "`" + ` lists the only chats whose messages are visible (empty: all chats).
    While not linked, ` + "`" + `pairing` + "`" + ` has the QR image path (store/qr.png) and, with
    WHATSAPP_PAIR_PHONE set, the code to enter on the phone.
    `,
	},
	{
		name: "reconnect_bridge", ann: annOwn, out: outResult,
		handle: reconnectBridge,
		params: []param{},
		doc: `Drop and reopen the bridge's connection to WhatsApp, for a session stuck in
    ` + "`" + `disconnected` + "`" + ` (see get_bridge_status). It does not help when the session needs
    a QR scan or was logged out. Allowed at most every 30 seconds.
    `,
	},
}
