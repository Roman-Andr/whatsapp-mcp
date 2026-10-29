package tools

// What a tool is: the entries of toolTable (table.go).

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type annotationKind int

// Tool annotations let clients auto-approve reads and ask before anything
// that reaches other people. Reads only touch the local store (downloads
// cache files under store/, history requests go to your own phone).
const (
	annRead       annotationKind = iota // local reads
	annReadRemote                       // reads that ask WhatsApp or the phone
	annSend                             // cannot be undone once the other side has seen it
	annSignal                           // visible to the other side but harmless to repeat
	annOwn                              // your own chat list: synced to your devices, invisible to others
)

func (k annotationKind) annotations() *mcp.ToolAnnotations {
	f, t := false, true
	switch k {
	case annRead:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &f}
	case annReadRemote:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &t}
	case annSend:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &t, IdempotentHint: false, OpenWorldHint: &t}
	default:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &t}
	}
}

func (k annotationKind) readOnly() bool { return k == annRead || k == annReadRemote }

type outputKind int

const (
	outText       outputKind = iota // str
	outOptText                      // Optional[str]
	outChats                        // List[Chat]
	outOptChat                      // Optional[Chat]
	outContacts                     // List[Contact]
	outResult                       // Result
	outSendResult                   // SendResult
	outDict                         // Dict[str, Any]
	outDictList                     // List[Dict[str, Any]]
	outImage                        // an image, or a dict explaining why not; no structured output
)

// param is one tool parameter. Parameters are required, nullable (default
// None) or have a default.
type param struct {
	name     string
	typ      string // string, integer, number, boolean, array (of strings)
	nullable bool
	required bool
	def      any
}

// handler runs a tool. What it returns depends on the tool's outputKind:
// string, *string, []*store.Chat, *store.Chat, []*store.Contact,
// *pyfmt.OMap, []*pyfmt.OMap, or an imageResult.
type handler func(ctx context.Context, c *Call) (any, error)

// toolSpec is one tool: its name, annotations, output kind, parameters,
// description and handler.
type toolSpec struct {
	name   string
	ann    annotationKind
	out    outputKind
	handle handler
	params []param
	doc    string
}
