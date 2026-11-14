package agent

import (
	"context"
	"sync"
	"time"
)

// notifier wakes waiters and subscribers when a session's screen or status changed.
type notifier struct {
	mu   sync.Mutex
	ch   chan struct{} // closed and replaced on every bump
	subs map[int]chan struct{}
	next int
	seq  uint64
}

func newNotifier() *notifier {
	return &notifier{ch: make(chan struct{}), subs: map[int]chan struct{}{}}
}

// Bump wakes everyone waiting on Changed and signals every subscriber (non-blocking: a
// subscriber that has a signal pending gets no second one).
func (n *notifier) Bump() {
	n.mu.Lock()
	n.seq++
	close(n.ch)
	n.ch = make(chan struct{})
	for _, c := range n.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	n.mu.Unlock()
}

// Seq counts the bumps so far.
func (n *notifier) Seq() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.seq
}

// Changed returns a channel that is closed at the next Bump. Take it before reading the state
// it guards, so no change is missed.
func (n *notifier) Changed() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Subscribe returns a channel that gets a signal after every Bump, and a cancel function.
func (n *notifier) Subscribe() (<-chan struct{}, func()) {
	c := make(chan struct{}, 1)
	n.mu.Lock()
	id := n.next
	n.next++
	n.subs[id] = c
	n.mu.Unlock()
	var once sync.Once
	return c, func() {
		once.Do(func() {
			n.mu.Lock()
			delete(n.subs, id)
			n.mu.Unlock()
		})
	}
}

// settleSource is what settle watches.
type settleSource interface {
	// State is the screen sequence, the input-inhibited state ("none", "system", "error") and
	// whether the connection is alive.
	State() (seq uint64, inhibited string, alive bool)
	// Changed is closed at the next change (see notifier.Changed).
	Changed() <-chan struct{}
}

// settleOpts tune settle.
type settleOpts struct {
	Quiet time.Duration // the screen must stay unchanged this long; 0 = SettleQuiet
	Max   time.Duration // give up after this; 0 = DefaultTimeout
	// Ready, when set, must also be true (for example "the screen has content").
	Ready func() bool
}

// settleResult is what settle saw last.
type settleResult struct {
	Settled   bool
	Inhibited string
	Seq       uint64
	Closed    bool // the connection ended while waiting
}

// settle waits for the host: the keyboard is unlocked ("none") and the screen sequence has
// not changed for opts.Quiet. An operator error ("error") never clears by itself, so it ends
// the wait as soon as the screen is quiet, with Settled false. It stops at opts.Max or when
// ctx ends (then the error is ctx's).
func settle(ctx context.Context, src settleSource, opts settleOpts) (settleResult, error) {
	quiet, max := opts.Quiet, opts.Max
	if quiet <= 0 {
		quiet = SettleQuiet
	}
	if max <= 0 {
		max = DefaultTimeout
	}
	start := time.Now()
	deadline := start.Add(max)
	lastSeq, _, _ := src.State()
	lastChange := start
	for {
		changed := src.Changed()
		seq, inh, alive := src.State()
		now := time.Now()
		if seq != lastSeq {
			lastSeq, lastChange = seq, now
		}
		res := settleResult{Inhibited: inh, Seq: seq}
		if !alive {
			res.Closed = true
			return res, nil
		}
		quietFor := now.Sub(lastChange)
		open := (inh == "none" || inh == "") && (opts.Ready == nil || opts.Ready())
		switch {
		case open && quietFor >= quiet:
			res.Settled = true
			return res, nil
		case inh == "error" && quietFor >= quiet:
			return res, nil
		case !now.Before(deadline):
			return res, nil
		}
		wait := deadline.Sub(now)
		if quietFor < quiet {
			if r := quiet - quietFor; r < wait {
				wait = r
			}
		} else if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond // a safety poll for conditions the notifier does not cover
		}
		t := time.NewTimer(wait)
		select {
		case <-changed:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return res, ctx.Err()
		}
		t.Stop()
	}
}

// waitFor waits until pred is true, polling on every change, for at most max. It reports
// whether pred matched; a dead connection ends the wait.
func waitFor(ctx context.Context, src settleSource, max time.Duration, pred func() bool) (bool, error) {
	if max <= 0 {
		max = DefaultTimeout
	}
	deadline := time.Now().Add(max)
	for {
		changed := src.Changed()
		if pred() {
			return true, nil
		}
		if _, _, alive := src.State(); !alive {
			return false, nil
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return false, nil
		}
		if wait > 200*time.Millisecond {
			wait = 200 * time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-changed:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return false, ctx.Err()
		}
		t.Stop()
	}
}
