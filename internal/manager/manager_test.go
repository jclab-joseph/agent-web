package manager

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/terminal"

	"github.com/jclab-joseph/agent-web/internal/model"
	"github.com/jclab-joseph/agent-web/internal/store"
)

// fakeProvider starts fakeSessions and records the options of every start.
type fakeProvider struct {
	mu     sync.Mutex
	starts []bridge.SessionOptions
	n      int
	last   *fakeSession
}

func (p *fakeProvider) Name() string { return "claude" }
func (p *fakeProvider) Info(context.Context) (bridge.Info, error) {
	return bridge.Info{Name: "claude", DisplayName: "Fake"}, nil
}

func (p *fakeProvider) Start(_ context.Context, o bridge.SessionOptions) (bridge.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, o)
	p.n++
	id := o.Resume
	if id == "" {
		id = fmt.Sprintf("cli-%d", p.n)
	}
	s := &fakeSession{id: id, events: make(chan bridge.Event, 100), done: make(chan struct{}), state: bridge.StateIdle}
	p.last = s
	return s, nil
}

func (p *fakeProvider) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.starts)
}

func (p *fakeProvider) lastStart() bridge.SessionOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts[len(p.starts)-1]
}

func (p *fakeProvider) session() *fakeSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

type fakeSession struct {
	id     string
	events chan bridge.Event
	done   chan struct{}

	mu     sync.Mutex
	state  bridge.State
	closed bool
}

func (s *fakeSession) ID() string                  { return s.id }
func (s *fakeSession) Provider() string            { return "claude" }
func (s *fakeSession) Events() <-chan bridge.Event { return s.events }
func (s *fakeSession) Settings() bridge.Settings   { return bridge.Settings{} }
func (s *fakeSession) Pending() []bridge.Request   { return nil }
func (s *fakeSession) Terminal() *terminal.Terminal {
	return nil
}
func (s *fakeSession) Done() <-chan struct{} { return s.done }

func (s *fakeSession) State() bridge.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *fakeSession) setState(st bridge.State) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
	s.events <- bridge.Event{Type: bridge.EventState, State: st, Time: time.Now()}
}

func (s *fakeSession) Send(_ context.Context, prompt string) error {
	now := time.Now()
	s.events <- bridge.Event{Type: bridge.EventUserMessage, Text: prompt, Time: now}
	s.events <- bridge.Event{Type: bridge.EventAssistantText, Text: "echo: " + prompt, Time: now}
	s.events <- bridge.Event{Type: bridge.EventTurnCompleted, Time: now}
	return nil
}

func (s *fakeSession) Respond(context.Context, string, bridge.Response) error { return nil }
func (s *fakeSession) Interrupt(context.Context) error                        { return nil }
func (s *fakeSession) Command(context.Context, string) error                  { return nil }
func (s *fakeSession) SetModel(context.Context, string) error                 { return nil }
func (s *fakeSession) SetEffort(context.Context, string) error                { return nil }
func (s *fakeSession) SetMode(context.Context, string) error                  { return nil }

func (s *fakeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.state = bridge.StateExited
	s.mu.Unlock()
	s.events <- bridge.Event{Type: bridge.EventExited, Text: "closed", Time: time.Now()}
	close(s.events)
	close(s.done)
	return nil
}

func newTestManager(t *testing.T, dbPath string, p *fakeProvider, idle time.Duration) *Manager {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m, err := New(context.Background(), Config{Store: st, IdleTimeout: idle, Providers: map[string]bridge.Provider{"claude": p}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func eventKinds(t *testing.T, s *Session) []string {
	t.Helper()
	evs, err := s.History(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		k := e.Type
		if e.Kind != "" {
			k += ":" + e.Kind
		}
		out = append(out, k)
	}
	return out
}

func TestIdleStopAndResume(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	p := &fakeProvider{}
	m := newTestManager(t, db, p, 300*time.Millisecond)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: t.TempDir(), Model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Summary(); !got.Running || got.Status != model.StatusIdle || got.CLISessionID != "cli-1" {
		t.Fatalf("after create: %+v", got)
	}
	if err := s.Send(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the title", func() bool { return s.Summary().Title == "hello" })

	// A busy CLI is never stopped, however long the turn takes.
	p.session().setState(bridge.StateBusy)
	time.Sleep(1500 * time.Millisecond)
	if !s.Summary().Running {
		t.Fatal("a busy CLI was stopped")
	}
	p.session().setState(bridge.StateIdle)

	waitFor(t, "the idle stop", func() bool { return !s.Summary().Running })
	sum := s.Summary()
	if sum.Status != model.StatusStopped || !sum.Resumable {
		t.Fatalf("after idle stop: %+v", sum)
	}

	// The next message resumes the same conversation with the stored settings.
	if err := s.Send(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	if n := p.startCount(); n != 2 {
		t.Fatalf("starts = %d, want 2", n)
	}
	if o := p.lastStart(); o.Resume != "cli-1" || o.Model != "haiku" {
		t.Fatalf("resume options = %+v", o)
	}
	waitFor(t, "the reply", func() bool { return s.Summary().LastSeq >= 9 })
	want := []string{"lifecycle:started", "user_message", "assistant_text", "turn_completed", "lifecycle:suspended",
		"lifecycle:resumed", "user_message", "assistant_text", "turn_completed"}
	if got := eventKinds(t, s); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	m.Close()
}

func TestSessionsSurviveRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	cwd := t.TempDir()
	p := &fakeProvider{}
	m := newTestManager(t, db, p, 0)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn", func() bool { return s.Summary().LastSeq >= 4 })
	m.Close()

	p2 := &fakeProvider{}
	m2 := newTestManager(t, db, p2, 0)
	list := m2.List()
	if len(list) != 1 || list[0].Status != model.StatusStopped || !list[0].Resumable || list[0].Title != "first" {
		t.Fatalf("after restart: %+v", list)
	}
	s2, err := m2.Get(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Send(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if o := p2.lastStart(); o.Resume != "cli-1" || o.Cwd != cwd {
		t.Fatalf("resume options = %+v", o)
	}
	kinds := eventKinds(t, s2)
	if kinds[4] != "lifecycle:shutdown" || kinds[5] != "lifecycle:resumed" {
		t.Fatalf("events: %v", kinds)
	}
	ws, _ := m2.Store().ListWorkdirs(ctx, 10)
	if len(ws) != 1 || ws[0].Path != cwd {
		t.Fatalf("workdirs: %+v", ws)
	}
	m2.Close()
}

func TestSubscribeReplaysWithoutGaps(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	p := &fakeProvider{}
	m := newTestManager(t, db, p, 0)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send(ctx, "one")
	waitFor(t, "the turn", func() bool { return s.Summary().LastSeq >= 4 })

	replay, hasMore, sub, cancel, err := s.Subscribe(ctx, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if hasMore || len(replay) != 2 || replay[0].Seq != 3 {
		t.Fatalf("replay = %+v hasMore=%v", replay, hasMore)
	}
	_ = s.Send(ctx, "two")
	var seqs []int64
	timeout := time.After(3 * time.Second)
	for len(seqs) < 3 {
		select {
		case u := <-sub.C:
			if u.Event != nil {
				seqs = append(seqs, u.Event.Seq)
			}
		case <-timeout:
			t.Fatalf("live events: %v", seqs)
		}
	}
	if seqs[0] != 5 || seqs[2] != 7 {
		t.Fatalf("live seqs = %v", seqs)
	}

	// Only the latest events are replayed when there are more than the limit.
	replay, hasMore, _, cancel2, err := s.Subscribe(ctx, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	cancel2()
	if !hasMore || len(replay) != 3 || replay[0].Seq != 5 {
		t.Fatalf("limited replay = %+v hasMore=%v", replay, hasMore)
	}
	m.Close()
}
