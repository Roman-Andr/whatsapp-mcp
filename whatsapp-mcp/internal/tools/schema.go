package tools

// Tool schemas in the pydantic-style shape clients were configured against:
// the same parameters, titles and defaults as before.

import (
	"strings"
	"unicode"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

// title is pydantic's field title: "chat_jid" -> "Chat Jid".
func title(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		if w != "" {
			r := []rune(w)
			words[i] = string(unicode.ToUpper(r[0])) + string(r[1:])
		}
	}
	return strings.Join(words, " ")
}

func typeSchema(typ string) *pyfmt.OMap {
	if typ == "array" {
		return pyfmt.NewOMap("items", pyfmt.NewOMap("type", "string"), "type", "array")
	}
	return pyfmt.NewOMap("type", typ)
}

func (p param) schema() *pyfmt.OMap {
	var s *pyfmt.OMap
	if p.nullable {
		s = pyfmt.NewOMap("anyOf", []any{typeSchema(p.typ), pyfmt.NewOMap("type", "null")})
	} else {
		s = typeSchema(p.typ)
	}
	if !p.required {
		s.Set("default", p.def)
	}
	s.Set("title", title(p.name))
	return s
}

var accountParam = param{name: "account", typ: "string", nullable: true}

const accountDoc = "        account: Which WhatsApp account to use (see list_accounts); omit for the default account\n"

// withAccount adds the `account` parameter and its line in the description.
func (t toolSpec) withAccount() toolSpec {
	t.params = append(append([]param{}, t.params...), accountParam)
	doc := strings.TrimRightFunc(t.doc, unicode.IsSpace)
	if strings.Contains(t.doc, "    Args:\n") {
		t.doc = doc + "\n" + accountDoc
	} else {
		t.doc = doc + "\n\n    Args:\n" + accountDoc
	}
	return t
}

func (t toolSpec) inputSchema() *pyfmt.OMap {
	props := pyfmt.NewOMap()
	var required []any
	for _, p := range t.params {
		props.Set(p.name, p.schema())
		if p.required {
			required = append(required, p.name)
		}
	}
	s := pyfmt.NewOMap("properties", props)
	if len(required) > 0 {
		s.Set("required", required)
	}
	return s.Set("title", t.name+"Arguments").Set("type", "object")
}

func nullable(s *pyfmt.OMap) *pyfmt.OMap {
	return pyfmt.NewOMap("anyOf", []any{s, pyfmt.NewOMap("type", "null")})
}

func optField(typ string) *pyfmt.OMap {
	return nullable(pyfmt.NewOMap("type", typ))
}

func chatDef() *pyfmt.OMap {
	withDefault := func(s *pyfmt.OMap, t string) *pyfmt.OMap { return s.Set("default", nil).Set("title", t) }
	props := pyfmt.NewOMap(
		"jid", pyfmt.NewOMap("title", "Jid", "type", "string"),
		"name", optField("string").Set("title", "Name"),
		"last_message_time", nullable(pyfmt.NewOMap("format", "date-time", "type", "string")).Set("title", "Last Message Time"),
		"last_message", withDefault(optField("string"), "Last Message"),
		"last_sender", withDefault(optField("string"), "Last Sender"),
		"last_is_from_me", withDefault(optField("boolean"), "Last Is From Me"),
		"unread_count", withDefault(optField("integer"), "Unread Count"),
		"archived", withDefault(optField("boolean"), "Archived"),
		"pinned", withDefault(optField("boolean"), "Pinned"),
		"muted_until", withDefault(optField("string"), "Muted Until"),
		"labels", pyfmt.NewOMap("items", pyfmt.NewOMap("type", "string"), "title", "Labels", "type", "array"),
	)
	return pyfmt.NewOMap("properties", props, "required", []any{"jid", "name", "last_message_time"}, "title", "Chat", "type", "object")
}

func contactDef() *pyfmt.OMap {
	props := pyfmt.NewOMap(
		"phone_number", pyfmt.NewOMap("title", "Phone Number", "type", "string"),
		"name", optField("string").Set("title", "Name"),
		"jid", pyfmt.NewOMap("title", "Jid", "type", "string"),
	)
	return pyfmt.NewOMap("properties", props, "required", []any{"phone_number", "name", "jid"}, "title", "Contact", "type", "object")
}

func wrapped(name string, result *pyfmt.OMap, defs *pyfmt.OMap) *pyfmt.OMap {
	s := pyfmt.NewOMap()
	if defs != nil {
		s.Set("$defs", defs)
	}
	return s.Set("properties", pyfmt.NewOMap("result", result)).Set("required", []any{"result"}).
		Set("title", name+"Output").Set("type", "object")
}

func (t toolSpec) outputSchema() *pyfmt.OMap {
	switch t.out {
	case outText:
		return wrapped(t.name, pyfmt.NewOMap("title", "Result", "type", "string"), nil)
	case outOptText:
		return wrapped(t.name, optField("string").Set("title", "Result"), nil)
	case outChats:
		return wrapped(t.name, pyfmt.NewOMap("items", pyfmt.NewOMap("$ref", "#/$defs/Chat"), "title", "Result", "type", "array"), pyfmt.NewOMap("Chat", chatDef()))
	case outOptChat:
		return wrapped(t.name, pyfmt.NewOMap("anyOf", []any{pyfmt.NewOMap("$ref", "#/$defs/Chat"), pyfmt.NewOMap("type", "null")}), pyfmt.NewOMap("Chat", chatDef()))
	case outContacts:
		return wrapped(t.name, pyfmt.NewOMap("items", pyfmt.NewOMap("$ref", "#/$defs/Contact"), "title", "Result", "type", "array"), pyfmt.NewOMap("Contact", contactDef()))
	case outResult:
		return pyfmt.NewOMap("description", "Outcome of a call that asks the bridge to change something.",
			"properties", pyfmt.NewOMap("success", pyfmt.NewOMap("title", "Success", "type", "boolean"), "message", pyfmt.NewOMap("title", "Message", "type", "string")),
			"required", []any{"success", "message"}, "title", "Result", "type", "object")
	case outSendResult:
		return pyfmt.NewOMap("properties", pyfmt.NewOMap("success", pyfmt.NewOMap("title", "Success", "type", "boolean"),
			"message", pyfmt.NewOMap("title", "Message", "type", "string"),
			"message_id", optField("string").Set("title", "Message Id")),
			"required", []any{"success", "message"}, "title", "SendResult", "type", "object")
	case outDict:
		return wrapped(t.name, pyfmt.NewOMap("additionalProperties", true, "title", "Result", "type", "object"), nil)
	case outDictList:
		return wrapped(t.name, pyfmt.NewOMap("items", pyfmt.NewOMap("additionalProperties", true, "type", "object"), "title", "Result", "type", "array"), nil)
	}
	return nil
}
