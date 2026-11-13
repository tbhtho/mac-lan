package main

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	videoQueueMaxFrames = 64
	videoQueueMaxBytes  = 8 << 20
	// Frames wait this long for a socket that has stopped accepting data. Wi-Fi drops out for a
	// few tenths of a second often enough that discarding at a quarter second threw the picture
	// away several times a minute; a backlog this size is sent and decoded in an instant once
	// the link returns, and the viewer shows the newest picture.
	videoQueueMaxAge = 500 * time.Millisecond
	// Spacing of keyframe requests while one is outstanding.
	videoKeyRequestInterval = 250 * time.Millisecond
	// A message larger than the socket write buffer goes out as several fragments, each its own
	// write and TLS record. Video frames are tens of kilobytes, so a 4 KiB buffer meant ten or
	// more writes per frame; this size sends an ordinary frame in one.
	videoWriteBuffer = 64 << 10
)

type queuedVideoFrame struct {
	data []byte
	at   time.Time
	key  bool
}

// videoQueue holds encoded frames, not independently decodable pictures. A
// short socket stall must preserve every H.264 predecessor. When bounded
// backpressure requires dropping data, only a new keyframe can begin a usable
// chain. JPEG pictures remain independent and use latest-only delivery.
type videoQueue struct {
	mu           sync.Mutex
	frames       []queuedVideoFrame
	bytes        int
	ready        chan struct{}
	h264         bool
	needsKey     bool
	lastSequence uint32
	// wantKey: encoded frames were discarded, so the viewer's picture stands still until a
	// keyframe arrives. The encoder sends one by itself only every two seconds; requestKey
	// asks the capture helper for one now. It is set before the writer starts.
	wantKey        bool
	lastKeyRequest time.Time
	requestKey     func()
}

func newVideoQueue() *videoQueue {
	return &videoQueue{ready: make(chan struct{}, 1), needsKey: true}
}

func (q *videoQueue) push(data []byte, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expire(now)
	encoded := len(data) >= 10 && (data[0] == 2 || data[0] == 3)
	key := encoded && data[0] == 2
	if !encoded {
		q.clear()
		q.h264, q.wantKey = false, false
	} else {
		sequence := binary.BigEndian.Uint32(data[1:5])
		if key {
			if !q.h264 {
				q.clear()
			}
			q.h264, q.needsKey, q.wantKey = true, false, false
		} else if !q.h264 || q.needsKey || sequence != q.lastSequence+1 {
			q.clear()
			q.wantKey = true
			// Wake an idle writer so it asks for the keyframe.
			select {
			case q.ready <- struct{}{}:
			default:
			}
			return
		}
		q.lastSequence = sequence
	}
	q.frames = append(q.frames, queuedVideoFrame{data: data, at: now, key: key})
	q.bytes += len(data)
	if len(q.frames) > videoQueueMaxFrames || q.bytes > videoQueueMaxBytes {
		q.recoverAtNewestKey(now)
	}
	if len(q.frames) > 0 {
		select {
		case q.ready <- struct{}{}:
		default:
		}
	}
}

func (q *videoQueue) pop(now time.Time) ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expire(now)
	if len(q.frames) == 0 {
		return nil, false
	}
	frame := q.frames[0]
	q.bytes -= len(frame.data)
	q.frames[0] = queuedVideoFrame{}
	q.frames = q.frames[1:]
	if len(q.frames) == 0 {
		q.frames = nil
	}
	return frame.data, true
}

func (q *videoQueue) clear() {
	q.frames = nil
	q.bytes = 0
	q.needsKey = true
}

func (q *videoQueue) expire(now time.Time) {
	if len(q.frames) > 0 && now.Sub(q.frames[0].at) > videoQueueMaxAge {
		q.recoverAtNewestKey(now)
	}
}

func (q *videoQueue) recoverAtNewestKey(now time.Time) {
	newest := -1
	for i := len(q.frames) - 1; i >= 0; i-- {
		if q.frames[i].key {
			newest = i
			break
		}
	}
	if newest < 0 || now.Sub(q.frames[newest].at) > videoQueueMaxAge {
		q.discard()
		return
	}
	for i := 0; i < newest; i++ {
		q.bytes -= len(q.frames[i].data)
		q.frames[i] = queuedVideoFrame{}
	}
	q.frames = q.frames[newest:]
	if len(q.frames) > videoQueueMaxFrames || q.bytes > videoQueueMaxBytes {
		q.discard()
	}
}

// discard drops everything queued because it can no longer be sent in time.
func (q *videoQueue) discard() {
	q.clear()
	if q.h264 {
		q.wantKey = true
	}
}

// keyframeDue reports, at most once per videoKeyRequestInterval, that discarded frames left
// the viewer waiting for a keyframe. The writer calls it when it has nothing to send, which
// is also the moment a stalled link has started accepting data again: a keyframe produced
// while the link is still stalled would only be discarded in turn.
func (q *videoQueue) keyframeDue(now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.wantKey || len(q.frames) > 0 || !q.lastKeyRequest.IsZero() && now.Sub(q.lastKeyRequest) < videoKeyRequestInterval {
		return false
	}
	q.lastKeyRequest = now
	return true
}

type statusMessage struct {
	line string
	sent chan struct{}
}

// The caller owns the only WebSocket writer and its per-message deadline. Check
// control/status messages before each video write so a captured-frame backlog
// cannot starve the pong used by the viewer's control lease.
// Status lines go first, then Mac audio (small, latency-sensitive), then video.
func writeQueuedVideo(done <-chan struct{}, frames *videoQueue, status <-chan statusMessage, audio <-chan []byte, write func(text bool, data []byte) error) {
	writeStatus := func(v statusMessage) bool {
		err := write(true, []byte(v.line))
		if v.sent != nil {
			close(v.sent)
		}
		return err == nil
	}
	for {
		select {
		case <-done:
			return
		default:
		}
		select {
		case v := <-status:
			if !writeStatus(v) {
				return
			}
			continue
		default:
		}
		select {
		case a := <-audio:
			if write(false, a) != nil {
				return
			}
			continue
		default:
		}
		if b, ok := frames.pop(time.Now()); ok {
			if write(false, b) != nil {
				return
			}
			continue
		}
		if frames.requestKey != nil && frames.keyframeDue(time.Now()) {
			frames.requestKey()
		}
		select {
		case <-done:
			return
		case <-frames.ready:
		case v := <-status:
			if !writeStatus(v) {
				return
			}
		case a := <-audio:
			if write(false, a) != nil {
				return
			}
		}
	}
}
