package main

import (
	"encoding/binary"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func encodedFixture(kind byte, sequence uint32, size int) []byte {
	if size < 10 {
		size = 10
	}
	b := make([]byte, size)
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:5], sequence)
	return b
}

func queueSequences(q *videoQueue, now time.Time) []uint32 {
	var sequences []uint32
	for {
		b, ok := q.pop(now)
		if !ok {
			return sequences
		}
		sequences = append(sequences, binary.BigEndian.Uint32(b[1:5]))
	}
}

func expectQueueSequences(t *testing.T, q *videoQueue, now time.Time, want ...uint32) {
	t.Helper()
	got := queueSequences(q, now)
	if len(got) != len(want) {
		t.Fatalf("queued sequences = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("queued sequences = %v, want %v", got, want)
		}
	}
}

func TestVideoQueueRetainsOrderedFramesAcrossKeyframes(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	for sequence, kind := range []byte{2, 3, 3, 2, 3} {
		q.push(encodedFixture(kind, uint32(sequence+1), 10), now)
	}
	expectQueueSequences(t, q, now, 1, 2, 3, 4, 5)
}

func TestVideoQueueJPEGIsLatestOnly(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	for sequence := uint32(1); sequence <= 10; sequence++ {
		q.push(encodedFixture(1, sequence, 10), now)
	}
	expectQueueSequences(t, q, now, 10)
}

func TestVideoQueueCountOverflowWaitsForKeyframe(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 10), now)
	for sequence := uint32(2); sequence <= videoQueueMaxFrames+2; sequence++ {
		q.push(encodedFixture(3, sequence, 10), now)
	}
	expectQueueSequences(t, q, now)
	q.push(encodedFixture(2, 100, 10), now)
	q.push(encodedFixture(3, 101, 10), now)
	expectQueueSequences(t, q, now, 100, 101)
}

func TestVideoQueueByteOverflowKeepsNewestCompleteKeySegment(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 3<<20), now)
	q.push(encodedFixture(3, 2, 3<<20), now)
	q.push(encodedFixture(2, 3, 3<<20), now)
	q.push(encodedFixture(3, 4, 10), now)
	expectQueueSequences(t, q, now, 3, 4)
	if q.bytes != 0 {
		t.Fatalf("drained queue retained %d bytes", q.bytes)
	}
}

func TestVideoQueueAgeDropsWholeDependentChain(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 10), now)
	q.push(encodedFixture(3, 2, 10), now.Add(10*time.Millisecond))
	q.push(encodedFixture(3, 3, 10), now.Add(videoQueueMaxAge+time.Millisecond))
	expectQueueSequences(t, q, now.Add(videoQueueMaxAge+time.Millisecond))
	q.push(encodedFixture(2, 4, 10), now.Add(time.Second))
	expectQueueSequences(t, q, now.Add(time.Second), 4)
}

func TestVideoQueueExpiredPrefixRetainsFreshKeySegment(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 10), now)
	q.push(encodedFixture(3, 2, 10), now.Add(10*time.Millisecond))
	q.push(encodedFixture(2, 3, 10), now.Add(videoQueueMaxAge-50*time.Millisecond))
	q.push(encodedFixture(3, 4, 10), now.Add(videoQueueMaxAge-40*time.Millisecond))
	expectQueueSequences(t, q, now.Add(videoQueueMaxAge+50*time.Millisecond), 3, 4)
}

func TestVideoQueueSourceSequenceGapWaitsForKeyframe(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 10), now)
	expectQueueSequences(t, q, now, 1)
	q.push(encodedFixture(3, 3, 10), now)
	q.push(encodedFixture(3, 4, 10), now)
	expectQueueSequences(t, q, now)
	q.push(encodedFixture(2, 5, 10), now)
	q.push(encodedFixture(3, 6, 10), now)
	expectQueueSequences(t, q, now, 5, 6)
}

func TestVideoQueueSequenceWrapIsContiguous(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, ^uint32(0), 10), now)
	q.push(encodedFixture(3, 0, 10), now)
	q.push(encodedFixture(3, 1, 10), now)
	expectQueueSequences(t, q, now, ^uint32(0), 0, 1)
}

func TestVideoQueueConcurrentProducerAndConsumerRemainBounded(t *testing.T) {
	q := newVideoQueue()
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for sequence := uint32(1); sequence <= 10000; sequence++ {
			kind := byte(3)
			if sequence%30 == 1 {
				kind = 2
			}
			q.push(encodedFixture(kind, sequence, 1024), time.Now())
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 10000; i++ {
			q.pop(time.Now())
		}
	}()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("video queue producer or consumer deadlocked")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.frames) > videoQueueMaxFrames || q.bytes > videoQueueMaxBytes {
		t.Fatalf("unbounded queue: %d frames, %d bytes", len(q.frames), q.bytes)
	}
}

func TestVideoWriterPrioritizesHeartbeatAfterStalledFrame(t *testing.T) {
	q := newVideoQueue()
	q.push(encodedFixture(2, 1, 10), time.Now())
	status := make(chan statusMessage, 4)
	done := make(chan struct{})
	entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var order []uint32
	go func() {
		defer close(finished)
		writeQueuedVideo(done, q, status, nil, func(text bool, data []byte) error {
			if text {
				order = append(order, 0)
				return nil
			}
			sequence := binary.BigEndian.Uint32(data[1:5])
			order = append(order, sequence)
			if sequence == 1 {
				close(entered)
				<-resume
			}
			if sequence == 3 {
				close(done)
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not begin the first frame")
	}
	q.push(encodedFixture(3, 2, 10), time.Now())
	q.push(encodedFixture(3, 3, 10), time.Now())
	sent := make(chan struct{})
	status <- statusMessage{line: `{"type":"pong"}`, sent: sent}
	close(resume)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("writer stalled after resuming")
	}
	if len(order) != 4 || order[0] != 1 || order[1] != 0 || order[2] != 2 || order[3] != 3 {
		t.Fatalf("writer order = %v, want [1 heartbeat 2 3]", order)
	}
	select {
	case <-sent:
	default:
		t.Fatal("reliable status acknowledgement was lost")
	}
}

// A short capture burst should remain decodable when the single socket writer
// has not run yet. Dropping an arbitrary encoded predecessor is not equivalent
// to dropping an independent JPEG: SequenceGate then rejects every dependent
// frame until the next keyframe, even after the writer has caught up.
func TestVideoBurstPreservesEncodedDependencies(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	const frames = 12
	s := &server{token: strings.Repeat("q", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(send func([]byte), _ func(string), events <-chan input) error {
			for sequence := uint32(1); sequence <= frames; sequence++ {
				kind := byte(3)
				if sequence == 1 {
					kind = 2
				}
				send(encodedFixture(kind, sequence, 10))
			}
			for range events {
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	for want := uint32(1); want <= frames; {
		kind, b, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("encoded burst stopped before sequence %d: %v", want, err)
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		got := binary.BigEndian.Uint32(b[1:5])
		if got != want {
			t.Fatalf("writer discarded encoded dependency: got sequence %d, want %d", got, want)
		}
		want++
	}
}

func TestMacAudioIsWrittenBeforeQueuedVideoAndNeverQueuedAsVideo(t *testing.T) {
	q := newVideoQueue()
	q.push(append([]byte{2, 0, 0, 0, 1, 0, 0, 0, 0}, 0xAA), time.Now())
	audio := make(chan []byte, 1)
	audio <- []byte{4, 0, 0, 0, 1, 0, 0, 0, 0, 1, 2}
	done := make(chan struct{})
	var kinds []byte
	writeQueuedVideo(done, q, make(chan statusMessage), audio, func(text bool, data []byte) error {
		kinds = append(kinds, data[0])
		if len(kinds) == 2 {
			close(done)
		}
		return nil
	})
	if len(kinds) != 2 || kinds[0] != 4 || kinds[1] != 2 {
		t.Fatalf("audio must go out first, got kinds %v", kinds)
	}
}

// Until a new keyframe arrives the viewer shows a frozen picture, and the encoder sends its
// own only every two seconds. The queue therefore says when it has discarded frames, so the
// host can ask the capture helper for a keyframe at once.
func TestVideoQueueAsksForKeyframeAfterDiscardingFrames(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	if q.keyframeDue(now) {
		t.Fatal("a queue that has not discarded anything asked for a keyframe")
	}
	q.push(encodedFixture(2, 1, 10), now)
	q.push(encodedFixture(3, 2, 10), now.Add(10*time.Millisecond))
	if q.keyframeDue(now.Add(20 * time.Millisecond)) {
		t.Fatal("a queue holding decodable frames asked for a keyframe")
	}
	late := now.Add(videoQueueMaxAge + 20*time.Millisecond)
	q.push(encodedFixture(3, 3, 10), late)
	expectQueueSequences(t, q, late)
	if !q.keyframeDue(late) {
		t.Fatal("discarded frames did not ask for a keyframe")
	}
	if q.keyframeDue(late.Add(videoKeyRequestInterval / 2)) {
		t.Fatal("keyframe requests were not spaced out")
	}
	if !q.keyframeDue(late.Add(videoKeyRequestInterval)) {
		t.Fatal("an unanswered keyframe request was not repeated")
	}
	answered := late.Add(videoKeyRequestInterval + 10*time.Millisecond)
	q.push(encodedFixture(2, 4, 10), answered)
	if q.keyframeDue(answered.Add(videoKeyRequestInterval)) {
		t.Fatal("the queue kept asking after the keyframe arrived")
	}
	expectQueueSequences(t, q, answered, 4)
}

func TestVideoQueueAsksForKeyframeAfterSourceGap(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(2, 1, 10), now)
	q.push(encodedFixture(3, 3, 10), now)
	if !q.keyframeDue(now) {
		t.Fatal("a broken frame chain did not ask for a keyframe")
	}
}

// JPEG pictures are independent; nothing needs to be re-requested for them.
func TestVideoQueueNeverAsksForKeyframeForJPEG(t *testing.T) {
	q := newVideoQueue()
	now := time.Now()
	q.push(encodedFixture(1, 1, 10), now)
	q.push(encodedFixture(1, 2, 10), now.Add(videoQueueMaxAge+time.Second))
	if q.keyframeDue(now.Add(videoQueueMaxAge + 2*time.Second)) {
		t.Fatal("a JPEG stream asked for a keyframe")
	}
}

// The request waits until the writer is free again: a keyframe produced while the link is
// still stalled would only be discarded in turn.
func TestVideoWriterAsksForKeyframeWhenStalledLinkResumes(t *testing.T) {
	q := newVideoQueue()
	requested := make(chan struct{}, 8)
	q.requestKey = func() { requested <- struct{}{} }
	began := time.Now()
	q.push(encodedFixture(2, 1, 10), began)
	done := make(chan struct{})
	defer close(done)
	entered, resume := make(chan struct{}), make(chan struct{})
	written := make(chan uint32, 8)
	go writeQueuedVideo(done, q, make(chan statusMessage), nil, func(text bool, data []byte) error {
		sequence := binary.BigEndian.Uint32(data[1:5])
		if sequence == 1 {
			close(entered)
			<-resume
		}
		written <- sequence
		return nil
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not begin the first frame")
	}
	q.push(encodedFixture(3, 2, 10), began.Add(10*time.Millisecond))
	q.push(encodedFixture(3, 3, 10), began.Add(videoQueueMaxAge+50*time.Millisecond))
	q.push(encodedFixture(3, 4, 10), began.Add(videoQueueMaxAge+60*time.Millisecond))
	select {
	case <-requested:
		t.Fatal("a keyframe was requested while the link was still stalled")
	case <-time.After(50 * time.Millisecond):
	}
	close(resume)
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("no keyframe was requested when the link resumed")
	}
	q.push(encodedFixture(2, 5, 10), time.Now())
	q.push(encodedFixture(3, 6, 10), time.Now())
	for _, want := range []uint32{1, 5, 6} {
		select {
		case got := <-written:
			if got != want {
				t.Fatalf("wrote sequence %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("sequence %d was not written", want)
		}
	}
}

// A drop that happens while the writer is idle must wake it to ask.
func TestVideoWriterAsksForKeyframeWhileIdle(t *testing.T) {
	q := newVideoQueue()
	requested := make(chan struct{}, 8)
	q.requestKey = func() { requested <- struct{}{} }
	done := make(chan struct{})
	defer close(done)
	written := make(chan uint32, 8)
	go writeQueuedVideo(done, q, make(chan statusMessage), nil, func(text bool, data []byte) error {
		written <- binary.BigEndian.Uint32(data[1:5])
		return nil
	})
	q.push(encodedFixture(2, 1, 10), time.Now())
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("the keyframe was not written")
	}
	q.push(encodedFixture(3, 3, 10), time.Now())
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("an idle writer did not ask for a keyframe after a broken frame chain")
	}
}

// End to end on the host: frames lost in the queue reach the capture side as a keyframe request.
func TestServerAsksCaptureForKeyframeAfterDiscardingFrames(t *testing.T) {
	var s *server
	s = &server{token: strings.Repeat("k", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(send func([]byte), _ func(string), events <-chan input) error {
			send(encodedFixture(2, 1, 10))
			send(encodedFixture(3, 3, 10))
			select {
			case <-s.keyframes:
				send(encodedFixture(2, 4, 10))
			case <-time.After(2 * time.Second):
				t.Error("the capture side was not asked for a keyframe")
			}
			for range events {
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		kind, b, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("the replacement keyframe never arrived: %v", err)
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		if sequence := binary.BigEndian.Uint32(b[1:5]); sequence == 4 {
			return
		} else if sequence != 1 {
			t.Fatalf("an undecodable frame was sent: sequence %d", sequence)
		}
	}
}
