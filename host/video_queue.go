package main

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	videoQueueMaxFrames = 64
	videoQueueMaxBytes  = 8 << 20
	videoQueueMaxAge    = 250 * time.Millisecond
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
		q.h264 = false
	} else {
		sequence := binary.BigEndian.Uint32(data[1:5])
		if key {
			if !q.h264 {
				q.clear()
			}
			q.h264, q.needsKey = true, false
		} else if !q.h264 || q.needsKey || sequence != q.lastSequence+1 {
			q.clear()
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
		q.clear()
		return
	}
	for i := 0; i < newest; i++ {
		q.bytes -= len(q.frames[i].data)
		q.frames[i] = queuedVideoFrame{}
	}
	q.frames = q.frames[newest:]
	if len(q.frames) > videoQueueMaxFrames || q.bytes > videoQueueMaxBytes {
		q.clear()
	}
}

type statusMessage struct {
	line string
	sent chan struct{}
}

// The caller owns the only WebSocket writer and its per-message deadline. Check
// control/status messages before each video write so a captured-frame backlog
// cannot starve the pong used by the viewer's control lease.
func writeQueuedVideo(done <-chan struct{}, frames *videoQueue, status <-chan statusMessage, write func(text bool, data []byte) error) {
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
		if b, ok := frames.pop(time.Now()); ok {
			if write(false, b) != nil {
				return
			}
			continue
		}
		select {
		case <-done:
			return
		case <-frames.ready:
		case v := <-status:
			if !writeStatus(v) {
				return
			}
		}
	}
}
