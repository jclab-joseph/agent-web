// Package model holds the types the HTTP API exposes. They mirror the
// clicodebridge types with JSON-friendly fields (decoded tool inputs instead
// of raw bytes) so the generated OpenAPI schema and TypeScript client stay
// simple, and are also what the event history stores.
package model

import (
	"encoding/json"
	"time"

	bridge "github.com/jclab-joseph/clicodebridge"
)

// Status is the session status the UI shows. "stopped" means the CLI is not
// running; the next action resumes it.
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle"
	StatusBusy     Status = "busy"
	StatusWaiting  Status = "waiting"
)

// EventLifecycle is an event type agent-web adds to the CLI's own: the CLI
// process was started, resumed or stopped. Kind says which.
const EventLifecycle = "lifecycle"

// Lifecycle kinds.
const (
	LifecycleStarted   = "started"   // a new conversation
	LifecycleResumed   = "resumed"   // the CLI was relaunched with --resume
	LifecycleSuspended = "suspended" // stopped after the idle timeout
	LifecycleStopped   = "stopped"   // stopped by the user
	LifecycleShutdown  = "shutdown"  // the server shut down
)

// Event is one entry of a session's history.
type Event struct {
	Seq         int64        `json:"seq" doc:"Sequence number within the session, increasing"`
	Type        string       `json:"type" enum:"user_message,assistant_text,thinking,tool_call,tool_result,files,turn_started,turn_completed,request,request_resolved,notice,restarted,diagnostic,error,exited,lifecycle"`
	Time        time.Time    `json:"time"`
	Text        string       `json:"text,omitempty"`
	Kind        string       `json:"kind,omitempty" doc:"Notice kind, diagnostic kind, lifecycle kind, or 'command' for a user_message that was a slash command"`
	Interrupted bool         `json:"interrupted,omitempty"`
	Tool        *ToolEvent   `json:"tool,omitempty"`
	Request     *Request     `json:"request,omitempty"`
	RequestID   string       `json:"requestId,omitempty"`
	Resolution  string       `json:"resolution,omitempty" enum:"client,external,cancelled"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Screen      string       `json:"screen,omitempty"`
}

// ToolEvent is a tool call or its result.
type ToolEvent struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
	IsError   bool   `json:"isError,omitempty"`
	Truncated bool   `json:"truncated,omitempty" doc:"Output was cut to keep the history small"`
}

// Attachment is an image or file shown or sent by the agent. Fetch it from
// /api/sessions/{id}/files/{fileId}.
type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Kind      string `json:"kind" enum:"image,video,audio,pdf,text,file"`
	Size      int64  `json:"size,omitempty"`
	Path      string `json:"path,omitempty"`
	Sent      bool   `json:"sent,omitempty" doc:"The agent delivered this file on purpose"`
	Caption   string `json:"caption,omitempty"`
}

// Request is a permission prompt or a set of questions waiting for an answer.
type Request struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind" enum:"permission,question"`
	Time       time.Time   `json:"time"`
	Title      string      `json:"title"`
	Permission *Permission `json:"permission,omitempty"`
	Questions  []Question  `json:"questions,omitempty"`
}

// Permission describes a permission prompt.
type Permission struct {
	ToolName string             `json:"toolName,omitempty"`
	Input    any                `json:"input,omitempty"`
	Detail   string             `json:"detail,omitempty"`
	Options  []PermissionOption `json:"options"`
}

// PermissionOption is one answer to a permission prompt.
type PermissionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind" enum:"allow_once,allow_always,deny,other"`
}

// Question is one question with options.
type Question struct {
	ID          string           `json:"id,omitempty"`
	Header      string           `json:"header,omitempty"`
	Text        string           `json:"text"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
	Options     []QuestionOption `json:"options"`
	AllowText   bool             `json:"allowText,omitempty"`
}

// QuestionOption is one choice.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Response answers a Request.
type Response struct {
	OptionID string   `json:"optionId,omitempty" doc:"Permission option to choose"`
	Decision string   `json:"decision,omitempty" enum:"allow_once,allow_always,deny,other" doc:"Choose the first option of this kind when optionId is empty"`
	Message  string   `json:"message,omitempty" doc:"Feedback for a denial"`
	Answers  []Answer `json:"answers,omitempty" doc:"Answers to a question request, index-aligned with its questions"`
	Cancel   bool     `json:"cancel,omitempty" doc:"Dismiss the request"`
}

// Answer answers one question with option labels and/or free text.
type Answer struct {
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// Settings are a session's model, effort and mode.
type Settings struct {
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	Mode             string `json:"mode"`
	RemoteControlURL string `json:"remoteControlUrl,omitempty"`
}

// LaunchOptions are the provider-specific options a session is started and
// resumed with. They are stored with the session.
type LaunchOptions struct {
	TrustWorkspace    bool   `json:"trustWorkspace,omitempty" doc:"Accept the workspace trust dialog automatically"`
	RemoteControl     string `json:"remoteControl,omitempty" enum:"default,on,off" doc:"Claude: Remote Control"`
	RemoteControlName string `json:"remoteControlName,omitempty"`
	Chrome            string `json:"chrome,omitempty" enum:"default,on,off" doc:"Claude: Claude in Chrome"`
	Interaction       string `json:"interaction,omitempty" enum:"hook,terminal" doc:"Claude: answer requests through hooks (default) or the TUI dialog"`
	ScreenReader      bool   `json:"screenReader,omitempty" doc:"Claude: run with --ax-screen-reader"`
	Bridge            string `json:"bridge,omitempty" enum:"auto,off" doc:"Claude: load the bridge mod"`
	Search            bool   `json:"search,omitempty" doc:"Codex: enable web search"`
}

// Session is a session's current summary.
type Session struct {
	ID           string        `json:"id"`
	Provider     string        `json:"provider"`
	Cwd          string        `json:"cwd"`
	Title        string        `json:"title"`
	CLISessionID string        `json:"cliSessionId" doc:"The CLI's conversation id"`
	Status       Status        `json:"status" enum:"stopped,starting,idle,busy,waiting"`
	Running      bool          `json:"running" doc:"The CLI process is running"`
	Resumable    bool          `json:"resumable" doc:"Starting the CLI continues the existing conversation"`
	Settings     Settings      `json:"settings"`
	Options      LaunchOptions `json:"options"`
	Pending      []Request     `json:"pending"`
	CreatedAt    time.Time     `json:"createdAt"`
	LastActiveAt time.Time     `json:"lastActiveAt"`
	AutoStopAt   *time.Time    `json:"autoStopAt,omitempty" doc:"When the idle CLI will be stopped, if nothing happens before"`
	LastSeq      int64         `json:"lastSeq" doc:"Sequence number of the latest event"`
}

// --- conversion from clicodebridge -------------------------------------------------

// maxToolOutput bounds stored tool output; huge file reads would otherwise
// bloat the history.
const maxToolOutput = 64 << 10

func rawToAny(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(r, &v) != nil {
		return string(r)
	}
	return v
}

// FromBridgeEvent converts a library event. Seq is assigned by the caller.
func FromBridgeEvent(ev bridge.Event) Event {
	e := Event{
		Type: string(ev.Type), Time: ev.Time, Text: ev.Text, Kind: ev.Kind, Interrupted: ev.Interrupted,
		RequestID: ev.RequestID, Resolution: string(ev.Resolution), Screen: ev.Screen,
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if ev.Tool != nil {
		t := &ToolEvent{ID: ev.Tool.ID, Name: ev.Tool.Name, Input: rawToAny(ev.Tool.Input), Output: ev.Tool.Output, IsError: ev.Tool.IsError}
		if len(t.Output) > maxToolOutput {
			t.Output, t.Truncated = t.Output[:maxToolOutput], true
		}
		e.Tool = t
	}
	if ev.Request != nil {
		r := FromBridgeRequest(*ev.Request)
		e.Request = &r
	}
	for _, a := range ev.Attachments {
		e.Attachments = append(e.Attachments, FromBridgeAttachment(a))
	}
	return e
}

// FromBridgeAttachment converts attachment metadata.
func FromBridgeAttachment(a bridge.Attachment) Attachment {
	size := a.Size
	if size == 0 {
		size = int64(len(a.Data))
	}
	return Attachment{ID: a.ID, Name: a.Name, MediaType: a.MediaType, Kind: string(a.Kind()), Size: size,
		Path: a.Path, Sent: a.Sent, Caption: a.Caption}
}

// FromBridgeRequest converts a request.
func FromBridgeRequest(r bridge.Request) Request {
	out := Request{ID: r.ID, Kind: string(r.Kind), Time: r.Time, Title: r.Title, Questions: []Question{}}
	if r.Permission != nil {
		p := &Permission{ToolName: r.Permission.ToolName, Input: rawToAny(r.Permission.Input), Detail: r.Permission.Detail,
			Options: []PermissionOption{}}
		for _, o := range r.Permission.Options {
			p.Options = append(p.Options, PermissionOption{ID: o.ID, Label: o.Label, Kind: string(o.Kind)})
		}
		out.Permission = p
	}
	for _, q := range r.Questions {
		mq := Question{ID: q.ID, Header: q.Header, Text: q.Text, MultiSelect: q.MultiSelect, AllowText: q.AllowText, Options: []QuestionOption{}}
		for _, o := range q.Options {
			mq.Options = append(mq.Options, QuestionOption{Label: o.Label, Description: o.Description})
		}
		out.Questions = append(out.Questions, mq)
	}
	return out
}

// ToBridge converts a response for Session.Respond.
func (r Response) ToBridge() bridge.Response {
	out := bridge.Response{OptionID: r.OptionID, Decision: bridge.PermissionKind(r.Decision), Message: r.Message, Cancel: r.Cancel}
	for _, a := range r.Answers {
		out.Answers = append(out.Answers, bridge.Answer{Selected: a.Selected, Text: a.Text})
	}
	return out
}
