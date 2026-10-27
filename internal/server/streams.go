package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/jclab-joseph/agent-web/internal/model"
)

// Replayed marks the end of the history replay of an event stream.
type Replayed struct {
	HasMore  bool  `json:"hasMore" doc:"Older events exist; fetch them with getHistory"`
	FirstSeq int64 `json:"firstSeq" doc:"Seq of the oldest replayed event (0 if none)"`
}

// TerminalReset is a full-screen snapshot; clear the terminal and write it.
type TerminalReset struct {
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Data string `json:"data" doc:"Base64 of the ANSI snapshot"`
}

// TerminalData is raw PTY output.
type TerminalData struct {
	Data string `json:"data" doc:"Base64 of the raw output"`
}

// TerminalOffline says the CLI is not running.
type TerminalOffline struct {
	Offline bool `json:"offline"`
}

const (
	initialReplay   = 300
	reconnectReplay = 10000
	pingInterval    = 20 * time.Second
)

func (s *Server) registerStreams() {
	sse.Register(s.api, huma.Operation{OperationID: "streamEvents", Method: http.MethodGet, Path: "/api/sessions/{id}/events",
		Tags: []string{"sessions"}, Summary: "Stream a session's events (SSE)",
		Description: "Sends a `summary`, replays stored events (the latest ones, or those after Last-Event-ID), sends `replayed`, " +
			"then streams new `event`s and `summary` changes. Each event's SSE id is its seq."},
		map[string]any{"summary": model.Session{}, "event": model.Event{}, "replayed": Replayed{}},
		func(ctx context.Context, in *struct {
			SessionPath
			LastEventID string `header:"Last-Event-ID"`
			After       int64  `query:"after" doc:"Replay events after this seq (like Last-Event-ID)"`
		}, send sse.Sender) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return
			}
			after, limit := in.After, initialReplay
			if v, err := strconv.ParseInt(in.LastEventID, 10, 64); err == nil && v > 0 {
				after = v
			}
			if after > 0 {
				limit = reconnectReplay
			}
			replay, hasMore, sub, cancel, err := sess.Subscribe(ctx, after, limit)
			if err != nil {
				return
			}
			defer cancel()
			if send(sse.Message{Data: sess.Summary(), Retry: 2000}) != nil {
				return
			}
			r := Replayed{HasMore: hasMore}
			for i, e := range replay {
				if i == 0 {
					r.FirstSeq = e.Seq
				}
				if send(sse.Message{IDString: strconv.FormatInt(e.Seq, 10), Data: e}) != nil {
					return
				}
			}
			if send.Data(r) != nil {
				return
			}
			ping := time.NewTicker(pingInterval)
			defer ping.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ping.C:
					if send.Comment("ping") != nil {
						return
					}
				case u, ok := <-sub.C:
					if !ok {
						return // fell behind or deleted; the browser reconnects with Last-Event-ID
					}
					var err error
					if u.Event != nil {
						err = send(sse.Message{IDString: strconv.FormatInt(u.Event.Seq, 10), Data: *u.Event})
					} else if u.Summary != nil {
						err = send.Data(*u.Summary)
					}
					if err != nil {
						return
					}
				}
			}
		})

	sse.Register(s.api, huma.Operation{OperationID: "streamTerminal", Method: http.MethodGet, Path: "/api/sessions/{id}/terminal",
		Tags: []string{"sessions"}, Summary: "Stream the CLI's terminal (SSE)",
		Description: "`reset` carries a full-screen snapshot (sent first and whenever the CLI is relaunched), `data` raw output, " +
			"`offline` that the CLI is not running."},
		map[string]any{"reset": TerminalReset{}, "data": TerminalData{}, "offline": TerminalOffline{}},
		func(ctx context.Context, in *SessionPath, send sse.Sender) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return
			}
			b64 := base64.StdEncoding.EncodeToString
			for {
				bs, changed := sess.Live()
				if bs == nil {
					if send(sse.Message{Data: TerminalOffline{Offline: true}, Retry: 2000}) != nil {
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-changed:
						continue
					}
				}
				t := bs.Terminal()
				snap, ch, cancel := t.Subscribe(1024)
				cols, rows := t.Size()
				if send.Data(TerminalReset{Cols: cols, Rows: rows, Data: b64(snap)}) != nil {
					cancel()
					return
				}
				ping := time.NewTicker(pingInterval)
			stream:
				for {
					select {
					case <-ctx.Done():
						cancel()
						ping.Stop()
						return
					case <-ping.C:
						if send.Comment("ping") != nil {
							cancel()
							return
						}
					case chunk, ok := <-ch:
						if !ok {
							break stream
						}
						if send.Data(TerminalData{Data: b64(chunk)}) != nil {
							cancel()
							return
						}
					}
				}
				cancel()
				ping.Stop()
				select {
				case <-ctx.Done():
					return
				case <-bs.Done():
				case <-time.After(300 * time.Millisecond): // relaunched with new settings, or we fell behind
				}
			}
		})
}
