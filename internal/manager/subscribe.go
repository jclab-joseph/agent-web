package manager

import (
	"context"

	"github.com/jclab-joseph/agent-web/internal/model"
)

// Update is one message to a subscriber: a new event or a new summary.
type Update struct {
	Event   *model.Event
	Summary *model.Session
}

// Subscriber receives a session's updates. C is closed when the subscriber
// falls behind (it should reconnect and replay from its last seq) or the
// session is deleted.
type Subscriber struct {
	C      <-chan Update
	c      chan Update
	closed bool
}

func (sub *Subscriber) closeLocked() {
	if !sub.closed {
		sub.closed = true
		close(sub.c)
	}
}

// subscriberBuffer is how far a subscriber may fall behind before it is cut
// off; it then replays from the database.
const subscriberBuffer = 1024

// Subscribe returns the stored events after seq `after` (at most limit, the
// newest ones when there are more; hasMore says so) and a subscriber for
// everything that follows, without gaps.
func (s *Session) Subscribe(ctx context.Context, after int64, limit int) (replay []model.Event, hasMore bool, sub *Subscriber, cancel func(), err error) {
	c := make(chan Update, subscriberBuffer)
	sub = &Subscriber{C: c, c: c}
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		return nil, false, nil, nil, ErrNotFound
	}
	// Events are stored and broadcast under s.mu, so everything up to upTo is
	// in the database and everything after it goes to the subscriber.
	upTo := s.seq
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	cancel = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[sub]; ok {
			delete(s.subs, sub)
			sub.closeLocked()
		}
	}
	if upTo > after {
		start := after
		if upTo-after > int64(limit) {
			start, hasMore = upTo-int64(limit), true
		}
		rows, err := s.m.cfg.Store.EventsAfter(ctx, s.id, start, int(upTo-start))
		if err != nil {
			cancel()
			return nil, false, nil, nil, err
		}
		replay = decodeEvents(rows)
	}
	return replay, hasMore, sub, cancel, nil
}

// broadcastLocked sends u to every subscriber; one that is full is cut off.
func (s *Session) broadcastLocked(u Update) {
	for sub := range s.subs {
		select {
		case sub.c <- u:
		default:
			sub.closeLocked()
			delete(s.subs, sub)
		}
	}
}

func (s *Session) broadcastSummary() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) == 0 {
		return
	}
	sum := s.summaryLocked()
	s.broadcastLocked(Update{Summary: &sum})
}
