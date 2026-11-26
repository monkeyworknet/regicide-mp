package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"regicide/engine"
	"regicide/replay"
)

// Recording: every applied action is appended to <data>/replays/<id>.jsonl, together
// with the state hash it produced. The file is written under the session lock, in the
// same order the actions were applied, so it is the game's own account of itself.
//
// A replay is the only artefact that contains hidden information (the hands), so it
// is never served to a spectator and it is deleted with its session.

func (s *Store) replayPath(id string) string {
	return filepath.Join(s.dir, "replays", id+".jsonl")
}

// openReplay opens the file for appending, creating it if this is the first write.
func (s *Store) openReplay(sess *Session) (*os.File, error) {
	if err := os.MkdirAll(filepath.Join(s.dir, "replays"), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(s.replayPath(sess.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// replayHeader describes the deal that has just been made.
func (s *Store) replayHeader(sess *Session) replay.Header {
	mode, room := "solo", ""
	players := []replay.Player{{Seat: 0, Name: "player"}}
	if sess.Room != nil {
		mode, room = "coop", sess.Room.Code
		players = players[:0]
		for _, seat := range sess.Room.Seats {
			players = append(players, replay.Player{Seat: seat.Seat, Name: seat.Name})
		}
	}
	return replay.Header{
		Mode:       mode,
		Room:       room,
		Seed:       sess.Game.Seed,
		SeedRandom: sess.Game.SeedRandom,
		Theme:      sess.Game.Theme,
		Jokers:     append([]string(nil), sess.Game.Jokers...),
		Rules:      sess.Game.Rules,
		NumPlayers: len(sess.Game.Players),
		Players:    players,
		StartedAt:  s.now(),
	}
}

// startReplay truncates any old file and writes the header. The caller holds the lock.
func (s *Store) startReplay(sess *Session) {
	f, err := s.openReplay(sess)
	if err != nil {
		return
	}
	defer f.Close()
	if err := f.Truncate(0); err != nil {
		return
	}
	_ = replay.NewWriter(f).WriteHeader(s.replayHeader(sess))
}

// lostReason is the readable loss reason, empty on a win.
func lostReason(g engine.Game) string {
	if g.Phase == engine.Lost {
		return g.LostReason.String()
	}
	return ""
}

// replayFile returns the recorded log for a session, or false when none was written.
func (s *Store) replayFile(id string) ([]byte, bool) {
	raw, err := os.ReadFile(s.replayPath(id))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// deleteReplay removes a session's log with the session itself.
func (s *Store) deleteReplay(id string) {
	_ = os.Remove(s.replayPath(id))
}

// ---- per-session write queue ----------------------------------------------
//
// Disk writes used to happen inside the session lock: a slow filesystem blocked
// every seat and every spectator poll for as long as the write took (F-29), a
// failed replay write was swallowed after the action had already been committed
// (F-8), and the two write paths disagreed about which seat acted (F-9).
//
// Now the decision and the mutation happen under the lock, and the writes are
// immutable jobs in one FIFO drained by one goroutine per session. Ordering is the
// queue's, so a retried action cannot duplicate a step, and no file write ever
// happens while the lock is held.

// writeQueueDepth bounds one session's backlog. A full queue means the disk is
// slower than the table, which is a bug to surface, not a policy to enforce.
const writeQueueDepth = 256

// writeEnqueueTimeout is how long an action waits for room in the queue before it
// is refused. Two seconds is longer than any write this server does.
const writeEnqueueTimeout = 2 * time.Second

type writeKind uint8

const (
	writeSessionFile writeKind = iota
	writeReplayStep
)

// writeJob is one unit of work. Everything it needs was captured under the lock, so
// the writer never reads mutable session state.
type writeJob struct {
	kind   writeKind
	seq    int
	snap   []byte       // writeSessionFile
	step   *replay.Step // writeReplayStep
	result *replay.Result
}

// startWriterLocked creates the session's queue and goroutine. Caller holds sess.mu.
func (s *Store) startWriterLocked(sess *Session) {
	if sess.outq != nil {
		return
	}
	outq := make(chan writeJob, writeQueueDepth)
	done := make(chan struct{})
	sess.outq = outq
	sess.writerDone = done
	// The channels are passed in, not read from the session: drainWrites clears the
	// fields, and ranging over a field that became nil would wedge this goroutine on
	// a nil channel forever (found by TestF9SoloStepRecordsTheActor).
	go s.runWriter(sess, outq, done)
}

// runWriter drains one session's queue, in order, for the life of the session.
func (s *Store) runWriter(sess *Session, outq <-chan writeJob, done chan<- struct{}) {
	defer close(done)
	for job := range outq {
		// Mark the session busy for the whole job, not just while it sits in the
		// queue: the writer dequeues before it touches the disk, so GC could see an
		// empty queue and delete the files of a write that was still in flight.
		sess.mu.Lock()
		sess.writing = true
		sess.mu.Unlock()
		if s.writeHook != nil {
			s.writeHook(sess, job)
		}
		err := s.runWrite(sess, job)
		sess.mu.Lock()
		sess.writing = false
		sess.mu.Unlock()
		if err != nil {
			// One retry: a transient failure (a full disk for a moment) should not
			// cost the log. A second failure marks the session degraded, which the
			// room view reports, so the host knows the download will be short.
			sess.mu.Lock()
			sess.writing = true
			sess.mu.Unlock()
			retryErr := s.runWrite(sess, job)
			sess.mu.Lock()
			sess.writing = false
			sess.mu.Unlock()
			if retryErr != nil {
				if s.logf != nil {
					s.logf("write queue: %s seq %d: %v", sess.ID, job.seq, retryErr)
				}
				sess.mu.Lock()
				sess.degraded = true
				sess.mu.Unlock()
			}
		}
	}
}

// runWrite performs one job. It never touches the session lock: everything it needs
// travels in the job, and sess.ID is immutable.
func (s *Store) runWrite(sess *Session, job writeJob) error {
	switch job.kind {
	case writeSessionFile:
		return writeFileAtomic(s.sessionPath(sess.ID), job.snap)
	case writeReplayStep:
		f, err := s.openReplay(sess)
		if err != nil {
			return err
		}
		defer f.Close()
		w := replay.NewWriter(f)
		if job.step != nil {
			if err := w.WriteStep(*job.step); err != nil {
				return err
			}
		}
		if job.result != nil {
			return w.WriteResult(*job.result)
		}
		return nil
	}
	return nil
}

// enqueueLocked hands one job to the session's writer. Caller holds sess.mu, which
// is what makes the order of two actions the order of their writes.
func (s *Store) enqueueLocked(sess *Session, job writeJob) bool {
	s.startWriterLocked(sess)
	// The common case is a queue with room; a timer there is one allocation and one
	// live timer per action for nothing.
	select {
	case sess.outq <- job:
		return true
	default:
	}
	timer := time.NewTimer(writeEnqueueTimeout)
	defer timer.Stop()
	select {
	case sess.outq <- job:
		return true
	case <-timer.C:
		// The disk has stopped keeping up. The action is already committed in
		// memory, so the honest move is to say the log is degraded, not to block
		// the table forever or to pretend the write happened.
		sess.degraded = true
		return false
	}
}

// enqueuePersistLocked queues a session snapshot.
func (s *Store) enqueuePersistLocked(sess *Session) {
	payload, err := json.Marshal(sess)
	if err != nil {
		sess.degraded = true
		return
	}
	s.enqueueLocked(sess, writeJob{kind: writeSessionFile, seq: sess.Seq, snap: payload})
}

// enqueueStepLocked queues one replay step (and the result line, if the game just
// ended). The caller holds the lock and has already applied the action.
func (s *Store) enqueueStepLocked(sess *Session, seat int, act engine.Action, g engine.Game) {
	raw, err := engine.MarshalAction(act)
	if err != nil {
		// A step that cannot be marshalled would leave a gap in the log while the
		// action is committed in memory: say so on the room view.
		sess.degraded = true
		return
	}
	step := &replay.Step{
		Seq: sess.Seq, Seat: seat, Action: raw, Hash: engine.StateHash(g),
		Turn: g.TurnNo, Phase: g.Phase.String(), Hands: engine.HandSizes(g),
	}
	job := writeJob{kind: writeReplayStep, seq: sess.Seq, step: step}
	if g.Phase == engine.Won || g.Phase == engine.Lost {
		job.result = &replay.Result{
			Phase: g.Phase.String(), Won: g.Phase == engine.Won,
			LostReason: lostReason(g), LostSeat: g.LostSeat,
			TurnNo: g.TurnNo, CastleLeft: len(g.Castle), JestersUsed: g.JestersUsed,
		}
	}
	s.enqueueLocked(sess, job)
}

// drainWrites waits for a session's queue to empty and stops its writer. A closed
// queue cannot be reused, so this is only for shutdown and deletion.
// drainWrites closes the queue and waits for the writer. It reports whether the writer
// finished: a timeout means a file may still be written, and a caller that is about to
// delete that file has to know (F-37).
func (s *Store) drainWrites(sess *Session) bool {
	sess.mu.Lock()
	outq, done := sess.outq, sess.writerDone
	sess.outq = nil
	sess.writerDone = nil
	sess.mu.Unlock()
	if outq == nil {
		return true
	}
	close(outq)
	select {
	case <-done:
		return true
	case <-time.After(10 * time.Second):
		if s.logf != nil {
			s.logf("write queue: %s did not drain in 10s; leaving its files alone", sess.ID)
		}
		return false
	}
}

// FlushWrites drains every session's queue. The server calls it on shutdown so no
// acknowledged action is lost.
func (s *Store) FlushWrites() {
	for _, sess := range s.sessionList() {
		s.drainWrites(sess)
	}
}

// ReplayDegraded reports whether a session's log fell behind. The room view carries
// it so the host is not told a short replay is the whole game.
func (s *Store) ReplayDegraded(sess *Session) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.degraded
}

// rotateReplay keeps the log of a finished game: the file becomes <id>.game<N>.jsonl
// and a counter on the session names the next one. Start used to truncate the file,
// which destroyed the log the result screen offers to download (D-47, F-25's sibling).
func (s *Store) rotateReplay(sess *Session) {
	current := s.replayPath(sess.ID)
	if _, err := os.Stat(current); err != nil {
		return
	}
	sess.games += 1
	_ = os.Rename(current, filepath.Join(s.dir, "replays", fmt.Sprintf("%s.game%d.jsonl", sess.ID, sess.games)))
}

// deleteReplays removes every log a session wrote: the current file and the numbered
// ones kept by play-again.
func (s *Store) deleteReplays(id string) {
	s.deleteReplay(id)
	matches, err := filepath.Glob(filepath.Join(s.dir, "replays", id+".game*.jsonl"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// replayTicketTTL is how long a viewer ticket lives. Short enough that a URL pasted
// into a chat is worthless by the time anyone reads it.
const replayTicketTTL = 2 * time.Minute

// ReplayTicket mints one viewer ticket for this session.
func (s *Store) ReplayTicket(sess *Session) (string, int) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.replayTickets == nil {
		sess.replayTickets = map[string]time.Time{}
	}
	now := s.now()
	for id, expires := range sess.replayTickets {
		if now.After(expires) {
			delete(sess.replayTickets, id)
		}
	}
	ticket := newID()
	sess.replayTickets[ticket] = now.Add(replayTicketTTL)
	return ticket, int(replayTicketTTL.Seconds())
}

// SessionByReplayTicket resolves a viewer ticket, or returns nothing when it has
// expired or was never issued.
func (s *Store) SessionByReplayTicket(ticket string) (*Session, bool) {
	if ticket == "" {
		return nil, false
	}
	now := s.now()
	for _, sess := range s.sessionList() {
		sess.mu.Lock()
		expires, ok := sess.replayTickets[ticket]
		if ok && now.After(expires) {
			delete(sess.replayTickets, ticket)
			ok = false
		}
		sess.mu.Unlock()
		if ok {
			return sess, true
		}
	}
	return nil, false
}

// flushWrites waits for a session's queue to empty without closing it. A reader that
// has to see every acknowledged action (the replay viewer, the host's download) calls
// this first: writes are queued, so the file is a few milliseconds behind the table.
// flushWrites waits for a session's queue to empty, and says whether it did. The old
// version spun for one second and then returned success implicitly, so the replay view
// could serve a log that was still being written (F-37).
func (s *Store) flushWrites(sess *Session) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		empty := sess.outq == nil || len(sess.outq) == 0
		busy := sess.writing
		sess.mu.Unlock()
		if empty && !busy {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
