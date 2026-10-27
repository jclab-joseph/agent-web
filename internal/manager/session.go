package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/terminal"

	"github.com/jclab-joseph/agent-web/internal/model"
	"github.com/jclab-joseph/agent-web/internal/store"
)

// maxInlineAttachment bounds the inline content (screenshots, images) stored
// per attachment.
const maxInlineAttachment = 32 << 20

// Session is one stored session and, while its CLI runs, the live process.
type Session struct {
	m  *Manager
	id string

	// opMu serializes starting and stopping the CLI.
	opMu sync.Mutex

	mu         sync.Mutex
	rec        store.Session
	opts       model.LaunchOptions
	run        *run // nil while the CLI is not running
	lastActive time.Time
	inflight   int // actions in progress; the CLI is never stopped under one
	seq        int64
	subs       map[*Subscriber]struct{}
	liveCh     chan struct{} // closed and replaced whenever run changes
	deleted    bool
}

// run is one CLI process lifetime.
type run struct {
	sess       bridge.Session
	done       chan struct{} // closed after the event pump has finished
	stopReason string        // lifecycle kind when we stopped it on purpose
}

func (m *Manager) newSession(ctx context.Context, rec store.Session) (*Session, error) {
	seq, err := m.cfg.Store.MaxSeq(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	s := &Session{m: m, id: rec.ID, rec: rec, seq: seq, lastActive: rec.LastActiveAt,
		subs: map[*Subscriber]struct{}{}, liveCh: make(chan struct{})}
	if err := json.Unmarshal([]byte(rec.Options), &s.opts); err != nil {
		m.log.Warn("bad stored options", "session", rec.ID, "err", err)
	}
	return s, nil
}

// ID is the session id.
func (s *Session) ID() string { return s.id }

// Summary returns the current state of the session.
func (s *Session) Summary() model.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryLocked()
}

func (s *Session) summaryLocked() model.Session {
	r := s.rec
	out := model.Session{
		ID: r.ID, Provider: r.Provider, Cwd: r.Cwd, Title: r.Title, CLISessionID: r.CLISessionID,
		Status: model.StatusStopped, Resumable: r.HasConversation && r.CLISessionID != "",
		Settings: model.Settings{Model: r.Model, Effort: r.Effort, Mode: r.Mode},
		Options:  s.opts, Pending: []model.Request{}, CreatedAt: r.CreatedAt, LastActiveAt: s.lastActive, LastSeq: s.seq,
	}
	if s.run != nil {
		bs := s.run.sess
		switch st := bs.State(); st {
		case bridge.StateExited:
		default:
			out.Status, out.Running = model.Status(st), true
		}
		out.Settings.RemoteControlURL = bs.Settings().RemoteControlURL
		for _, p := range bs.Pending() {
			out.Pending = append(out.Pending, model.FromBridgeRequest(p))
		}
		if out.Status == model.StatusIdle && s.m.cfg.IdleTimeout > 0 && s.inflight == 0 {
			at := s.lastActive.Add(s.m.cfg.IdleTimeout)
			out.AutoStopAt = &at
		}
	}
	return out
}

// --- activity and the idle timeout -------------------------------------------------

// touch records activity now.
func (s *Session) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

// begin and end bracket a user action: the CLI is not stopped while one
// runs, and the idle timeout restarts after it.
func (s *Session) begin() {
	s.mu.Lock()
	s.inflight++
	s.lastActive = time.Now()
	s.mu.Unlock()
}

func (s *Session) end() {
	s.mu.Lock()
	s.inflight--
	s.lastActive = time.Now()
	s.rec.LastActiveAt = s.lastActive
	rec := s.rec
	s.mu.Unlock()
	s.persist(rec)
	s.broadcastSummary()
}

// idleExpired reports whether the CLI runs, has been idle for the timeout,
// and no action is in progress. A busy or waiting CLI counts as active.
func (s *Session) idleExpired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || s.run.stopReason != "" || s.inflight > 0 {
		return false
	}
	switch s.run.sess.State() {
	case bridge.StateIdle:
		return now.Sub(s.lastActive) >= s.m.cfg.IdleTimeout
	case bridge.StateExited:
		return false
	default: // starting, busy, waiting for an answer
		s.lastActive = now
		return false
	}
}

// --- CLI lifecycle --------------------------------------------------------------------

// ensureRunning returns the running CLI session, starting (or resuming) it
// if needed.
func (s *Session) ensureRunning(ctx context.Context) (bridge.Session, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	r, deleted := s.run, s.deleted
	s.mu.Unlock()
	if deleted {
		return nil, ErrNotFound
	}
	if r != nil {
		select {
		case <-r.sess.Done():
			<-r.done // exited on its own; let the pump finish
		default:
			return r.sess, nil
		}
	}
	return s.startLocked(ctx)
}

// startLocked launches the CLI. Caller holds opMu.
func (s *Session) startLocked(ctx context.Context) (bridge.Session, error) {
	s.mu.Lock()
	rec, opts := s.rec, s.opts
	s.mu.Unlock()
	p := s.m.cfg.Providers[rec.Provider]
	if p == nil {
		return nil, fmt.Errorf("provider %q is not available", rec.Provider)
	}
	so := bridge.SessionOptions{Cwd: rec.Cwd, Model: rec.Model, Effort: rec.Effort, Mode: rec.Mode,
		Provider: s.m.providerOptions(rec.Provider, opts)}
	resume := rec.HasConversation && rec.CLISessionID != ""
	if resume {
		so.Resume = rec.CLISessionID
	}
	bs, err := p.Start(ctx, so)
	if err != nil {
		s.record(model.Event{Type: string(bridge.EventError), Time: time.Now(), Text: "Could not start the CLI: " + err.Error()})
		return nil, err
	}
	r := &run{sess: bs, done: make(chan struct{})}
	s.mu.Lock()
	s.run = r
	s.lastActive = time.Now()
	if id := bs.ID(); id != "" {
		s.rec.CLISessionID = id
	}
	s.signalLiveLocked()
	rec = s.rec
	s.mu.Unlock()
	s.persist(rec)

	kind, text := model.LifecycleStarted, "CLI started"
	if resume {
		kind, text = model.LifecycleResumed, "CLI resumed the conversation"
	}
	s.m.log.Info(text, "session", s.id, "provider", rec.Provider, "cliSession", rec.CLISessionID)
	s.record(model.Event{Type: model.EventLifecycle, Kind: kind, Time: time.Now(), Text: text})
	go s.pump(r)
	s.broadcastSummary()
	return bs, nil
}

// stopCLI closes the CLI if it runs. kind is the lifecycle event recorded
// for it; for LifecycleSuspended the idle condition is re-checked first, so
// an action that arrived meanwhile wins.
func (s *Session) stopCLI(kind string) bool {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	r := s.run
	if r == nil || r.stopReason != "" {
		s.mu.Unlock()
		return false
	}
	if kind == model.LifecycleSuspended {
		idle := s.inflight == 0 && r.sess.State() == bridge.StateIdle && time.Since(s.lastActive) >= s.m.cfg.IdleTimeout
		if !idle {
			s.mu.Unlock()
			return false
		}
	}
	r.stopReason = kind
	s.mu.Unlock()
	_ = r.sess.Close()
	<-r.done
	return true
}

// pump records the CLI's events until it exits.
func (s *Session) pump(r *run) {
	for ev := range r.sess.Events() {
		s.handle(r, ev)
	}
	s.mu.Lock()
	if s.run == r {
		s.run = nil
		s.signalLiveLocked()
	}
	s.mu.Unlock()
	close(r.done)
	s.broadcastSummary()
}

var lifecycleText = map[string]string{
	model.LifecycleSuspended: "CLI stopped after being idle; it resumes with your next message",
	model.LifecycleStopped:   "CLI stopped; it resumes with your next message",
	model.LifecycleShutdown:  "CLI stopped because the server shut down",
}

func (s *Session) handle(r *run, ev bridge.Event) {
	switch ev.Type {
	case bridge.EventState:
		s.broadcastSummary()
		return
	case bridge.EventSettings:
		st := r.sess.Settings()
		s.update(func(rec *store.Session) { rec.Model, rec.Effort, rec.Mode = st.Model, st.Effort, st.Mode })
		return
	case bridge.EventSessionID:
		id := r.sess.ID()
		s.update(func(rec *store.Session) { rec.CLISessionID = id })
		return
	case bridge.EventExited:
		s.mu.Lock()
		reason := r.stopReason
		s.mu.Unlock()
		if reason != "" {
			s.record(model.Event{Type: model.EventLifecycle, Kind: reason, Time: ev.Time, Text: lifecycleText[reason]})
			return
		}
		// An unexpected exit: the last screen usually says why.
		if t := r.sess.Terminal(); t != nil && ev.Screen == "" {
			ev.Screen = strings.TrimRight(t.Text(), " \n")
		}
	case bridge.EventUserMessage, bridge.EventTurnStarted:
		text := ev.Text
		s.update(func(rec *store.Session) {
			rec.HasConversation = true
			if rec.Title == "" && ev.Type == bridge.EventUserMessage && ev.Kind != "command" {
				rec.Title = titleFrom(text)
			}
		})
	}
	switch ev.Type {
	case bridge.EventNotice, bridge.EventDiagnostic, bridge.EventRestarted, bridge.EventExited:
		// not activity: Claude sends idle notifications while it waits
	default:
		s.touch()
	}
	for _, a := range ev.Attachments {
		sa := store.Attachment{ID: a.ID, Name: a.Name, MediaType: a.MediaType, Size: a.Size, Path: a.Path, CreatedAt: time.Now()}
		if len(a.Data) > 0 && len(a.Data) <= maxInlineAttachment {
			sa.Data, sa.Size = a.Data, int64(len(a.Data))
		}
		if err := s.m.cfg.Store.PutAttachment(context.Background(), s.id, sa); err != nil {
			s.m.log.Warn("store attachment", "session", s.id, "err", err)
		}
	}
	s.record(model.FromBridgeEvent(ev))
	switch ev.Type {
	case bridge.EventRequest, bridge.EventRequestResolved, bridge.EventTurnCompleted, bridge.EventExited:
		s.broadcastSummary()
	}
}

func titleFrom(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > 80 {
		text = string([]rune(text)[:80]) + "…"
	}
	return text
}

// update changes the stored record and broadcasts the new summary.
func (s *Session) update(f func(*store.Session)) {
	s.mu.Lock()
	before := s.rec
	f(&s.rec)
	changed := s.rec != before
	if changed {
		s.rec.UpdatedAt = time.Now()
	}
	rec := s.rec
	s.mu.Unlock()
	if changed {
		s.persist(rec)
	}
	s.broadcastSummary()
}

func (s *Session) persist(rec store.Session) {
	s.mu.Lock()
	deleted := s.deleted
	s.mu.Unlock()
	if deleted {
		return
	}
	if err := s.m.cfg.Store.UpdateSession(context.Background(), rec); err != nil {
		s.m.log.Warn("save session", "session", s.id, "err", err)
	}
}

// record stores an event and sends it to subscribers.
func (s *Session) record(e model.Event) {
	if e.Type == string(bridge.EventError) || e.Type == string(bridge.EventExited) {
		s.m.log.Warn("session "+e.Type, "session", s.id, "text", e.Text)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleted {
		return
	}
	s.seq++
	e.Seq = s.seq
	data, err := json.Marshal(e)
	if err == nil {
		err = s.m.cfg.Store.AppendEvent(context.Background(), s.id, store.EventRow{Seq: e.Seq, Type: e.Type, Time: e.Time, Data: data})
	}
	if err != nil {
		s.m.log.Error("store event", "session", s.id, "err", err)
	}
	s.broadcastLocked(Update{Event: &e})
}

// --- actions ------------------------------------------------------------------------------

// act runs f against the running CLI, starting or resuming it first.
func (s *Session) act(ctx context.Context, f func(bridge.Session) error) error {
	s.begin()
	defer s.end()
	bs, err := s.ensureRunning(ctx)
	if err != nil {
		return err
	}
	return f(bs)
}

// running returns the CLI session if it runs, without starting it.
func (s *Session) running() (bridge.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || s.run.stopReason != "" {
		return nil, ErrNotRunning
	}
	return s.run.sess, nil
}

// Start starts or resumes the CLI.
func (s *Session) Start(ctx context.Context) error {
	return s.act(ctx, func(bridge.Session) error { return nil })
}

// Stop stops the CLI. The session can be resumed.
func (s *Session) Stop() { s.stopCLI(model.LifecycleStopped) }

// Send submits a prompt, resuming the CLI if needed.
func (s *Session) Send(ctx context.Context, text string) error {
	return s.act(ctx, func(bs bridge.Session) error { return bs.Send(ctx, text) })
}

// Command runs a slash command, resuming the CLI if needed.
func (s *Session) Command(ctx context.Context, command string) error {
	return s.act(ctx, func(bs bridge.Session) error { return bs.Command(ctx, command) })
}

// Respond answers a pending request. Requests do not survive a CLI restart,
// so this never starts the CLI.
func (s *Session) Respond(ctx context.Context, requestID string, resp model.Response) error {
	s.begin()
	defer s.end()
	bs, err := s.running()
	if err != nil {
		return err
	}
	return bs.Respond(ctx, requestID, resp.ToBridge())
}

// Interrupt stops the running turn; a no-op when the CLI is not running.
func (s *Session) Interrupt(ctx context.Context) error {
	s.begin()
	defer s.end()
	bs, err := s.running()
	if err != nil {
		return nil
	}
	return bs.Interrupt(ctx)
}

// SettingsChange changes some settings; nil fields stay.
type SettingsChange struct {
	Model, Effort, Mode *string
}

// SetSettings applies a settings change. A stopped session only stores it;
// it is used when the CLI next starts.
func (s *Session) SetSettings(ctx context.Context, c SettingsChange) error {
	s.begin()
	defer s.end()
	bs, err := s.running()
	if err != nil {
		s.update(func(rec *store.Session) {
			if c.Model != nil {
				rec.Model = *c.Model
			}
			if c.Effort != nil {
				rec.Effort = *c.Effort
			}
			if c.Mode != nil {
				rec.Mode = *c.Mode
			}
		})
		return nil
	}
	if c.Mode != nil {
		if err := bs.SetMode(ctx, *c.Mode); err != nil {
			return err
		}
	}
	if c.Model != nil {
		if err := bs.SetModel(ctx, *c.Model); err != nil {
			return err
		}
	}
	if c.Effort != nil {
		if err := bs.SetEffort(ctx, *c.Effort); err != nil {
			return err
		}
	}
	return nil
}

// Rename sets the title.
func (s *Session) Rename(title string) {
	title = strings.TrimSpace(title)
	s.update(func(rec *store.Session) { rec.Title = title })
}

// TerminalInput writes keystrokes to the running CLI.
func (s *Session) TerminalInput(data string) error {
	bs, err := s.running()
	if err != nil {
		return err
	}
	s.touch()
	_, err = bs.Terminal().WriteString(data)
	return err
}

// Live returns the running CLI (nil if none) and a channel that is closed
// when that changes.
func (s *Session) Live() (bridge.Session, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return nil, s.liveCh
	}
	return s.run.sess, s.liveCh
}

// Terminal returns the running CLI's terminal.
func (s *Session) Terminal() (*terminal.Terminal, error) {
	bs, err := s.running()
	if err != nil {
		return nil, err
	}
	return bs.Terminal(), nil
}

// Attachment returns a stored attachment. Only attachments the session's
// events named are stored, so the server never opens a path a client chose.
func (s *Session) Attachment(ctx context.Context, id string) (store.Attachment, error) {
	return s.m.cfg.Store.GetAttachment(ctx, s.id, id)
}

// History returns stored events before seq (0 = latest), oldest first.
func (s *Session) History(ctx context.Context, before int64, limit int) ([]model.Event, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.m.cfg.Store.EventsBefore(ctx, s.id, before, limit)
	if err != nil {
		return nil, err
	}
	return decodeEvents(rows), nil
}

func decodeEvents(rows []store.EventRow) []model.Event {
	out := make([]model.Event, 0, len(rows))
	for _, r := range rows {
		var e model.Event
		if json.Unmarshal(r.Data, &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *Session) signalLiveLocked() {
	close(s.liveCh)
	s.liveCh = make(chan struct{})
}

func (s *Session) markDeleted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = true
	for sub := range s.subs {
		sub.closeLocked()
		delete(s.subs, sub)
	}
	s.signalLiveLocked()
}
