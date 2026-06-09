package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"

	"mermaid-collab/models"
)

// y-websocket protocol message types (first varuint of every message).
const (
	messageSync           = 0
	messageAwareness      = 1
	messageAuth           = 2
	messageQueryAwareness = 3
)

// y-protocols/sync sub-message types (second varuint when messageSync).
const (
	syncStep1  = 0
	syncStep2  = 1
	syncUpdate = 2
)

// Operational + safety limits.
const (
	maxMessageBytes   = 1 << 20          // 1 MiB: reject oversized frames (DoS guard)
	maxClientsPerRoom = 50               // cap fan-out per room
	maxRoomBytes      = 32 << 20         // 32 MiB: hard ceiling on a room's update log
	compactInterval   = 30 * time.Second // how often we consider compacting
	compactThreshold  = 64               // compact once the log exceeds this many entries
	compactMinAge     = 10 * time.Second // only ask clients synced for at least this long
	compactTimeout    = 2 * time.Minute  // abandon a compaction whose target never replies
	pingInterval      = 25 * time.Second // keepalive ping cadence
	pongWait          = 60 * time.Second // drop a connection with no pong within this window
	writeWait         = 10 * time.Second // per-write deadline
)

// emptyDocUpdate is Y.encodeStateAsUpdate(new Y.Doc()) — a valid no-op update.
// Used as a SyncStep2 reply for empty rooms so a joiner still flips to `synced`.
var emptyDocUpdate = []byte{0x00, 0x00}

// Client is a single WebSocket connection bound to one room.
type Client struct {
	conn        *websocket.Conn
	send        chan []byte
	roomID      string
	userID      string // random UUID assigned on connect
	connectedAt time.Time

	// awaitingFull marks that this client was asked (for compaction) to return a
	// full-document snapshot; its next SyncStep2 is treated as authoritative.
	awaitingFull atomic.Bool
	compactMark  atomic.Int64 // log length when the compaction request was issued
	lossy        atomic.Bool  // a frame was ever dropped to this client (never a compaction source)
}

// trySend enqueues a frame without blocking the room loop; on a full buffer the
// client is flagged lossy so it is never trusted as a compaction snapshot source.
func (c *Client) trySend(b []byte) {
	select {
	case c.send <- b:
	default:
		c.lossy.Store(true)
	}
}

// envelope carries a payload plus its sender so the room relays to everyone
// except the originator. (The documented payload is the raw []byte Yjs frame.)
type envelope struct {
	sender *Client
	data   []byte
}

// Room fans messages out to every client and keeps an ordered, opaque log of
// Yjs updates. The server never CRDT-merges bytes itself — clients (which run
// the real Yjs engine) converge by replaying the log; the server only stores
// and periodically compacts it from a trusted, fully-synced client.
type Room struct {
	id         string
	clients    map[*Client]bool
	broadcast  chan envelope
	register   chan *Client
	unregister chan *Client

	updateLog [][]byte // [0] is the compaction base; the rest are later updates
	logBytes  int      // total bytes across updateLog (enforces maxRoomBytes)

	compacting   bool      // a compaction request is in flight
	compactStart time.Time // when it was issued (for compactTimeout)
	mu           sync.RWMutex
}

// Hub owns all rooms and bridges them to PostgreSQL for load/persist.
type Hub struct {
	rooms map[string]*Room
	mu    sync.RWMutex
	db    *sql.DB
}

// NewHub builds a hub backed by the given DB pool.
func NewHub(db *sql.DB) *Hub {
	return &Hub{rooms: make(map[string]*Room), db: db}
}

// openRoom returns the live room for id, or nil if no such room exists in the
// DB (rooms are created via REST, never implicitly over WebSocket).
func (h *Hub) openRoom(id string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	if r, ok := h.rooms[id]; ok {
		return r
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	room, err := models.GetRoom(ctx, h.db, id)
	if err != nil {
		slog.Error("openRoom lookup", "room", id, "err", err)
		return nil
	}
	if room == nil {
		return nil // unknown room — reject the connection
	}

	r := &Room{
		id:         id,
		clients:    make(map[*Client]bool),
		broadcast:  make(chan envelope, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
	if len(room.YjsState) > 0 {
		r.updateLog = decodeLog(room.YjsState)
		for _, u := range r.updateLog {
			r.logBytes += len(u)
		}
	}

	h.rooms[id] = r
	go r.run(h)
	return r
}

// run is the room's single-goroutine event loop; all client-map mutation
// happens here, so reads elsewhere only take the RWMutex.
func (r *Room) run(h *Hub) {
	ticker := time.NewTicker(compactInterval)
	defer ticker.Stop()

	for {
		select {
		case c := <-r.register:
			r.mu.Lock()
			r.clients[c] = true
			r.mu.Unlock()
			slog.Info("client joined", "room", r.id, "user", c.userID)

		case c := <-r.unregister:
			r.mu.Lock()
			if _, ok := r.clients[c]; ok {
				delete(r.clients, c)
				close(c.send)
			}
			if c.awaitingFull.Load() {
				r.compacting = false // compaction target left; unblock future attempts
			}
			empty := len(r.clients) == 0
			r.mu.Unlock()
			slog.Info("client left", "room", r.id, "user", c.userID)

			if empty {
				h.persistRoom(r)
				h.mu.Lock()
				delete(h.rooms, r.id)
				h.mu.Unlock()
				return
			}

		case env := <-r.broadcast:
			r.mu.RLock()
			for c := range r.clients {
				if c == env.sender {
					continue // never echo a frame back to its origin
				}
				select {
				case c.send <- env.data:
				default:
					c.lossy.Store(true)
					slog.Warn("dropping slow client", "room", r.id, "user", c.userID)
				}
			}
			r.mu.RUnlock()

		case <-ticker.C:
			r.maybeCompact()
		}
	}
}

// appendUpdate stores an opaque Yjs update, enforcing the per-room byte ceiling.
func (r *Room) appendUpdate(u []byte) {
	if len(u) == 0 || bytes.Equal(u, emptyDocUpdate) {
		return // nothing meaningful to retain
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logBytes+len(u) > maxRoomBytes {
		slog.Warn("room update log at capacity, dropping update", "room", r.id, "bytes", r.logBytes)
		return
	}
	r.updateLog = append(r.updateLog, append([]byte(nil), u...))
	r.logBytes += len(u)
}

// compactWith replaces everything up to the compaction mark with a single full
// snapshot, then re-appends the tail recorded since the request was issued.
// Duplicated tail updates are idempotent on replay, so this never loses data.
func (r *Room) compactWith(c *Client, snapshot []byte) {
	if len(snapshot) <= len(emptyDocUpdate) {
		// Defensive: never collapse a room into an empty snapshot.
		r.mu.Lock()
		r.compacting = false
		r.mu.Unlock()
		return
	}
	mark := int(c.compactMark.Load())

	r.mu.Lock()
	defer r.mu.Unlock()
	if mark < 0 || mark > len(r.updateLog) {
		mark = len(r.updateLog)
	}
	tail := r.updateLog[mark:]
	newLog := make([][]byte, 0, 1+len(tail))
	newLog = append(newLog, append([]byte(nil), snapshot...))
	newLog = append(newLog, tail...)
	r.updateLog = newLog
	r.logBytes = 0
	for _, u := range newLog {
		r.logBytes += len(u)
	}
	r.compacting = false
	slog.Info("compacted room log", "room", r.id, "entries", len(newLog), "bytes", r.logBytes)
}

// maybeCompact asks one trusted, fully-synced client for a fresh full snapshot
// when the log has grown large. Only clients connected past compactMinAge that
// have never had a frame dropped are eligible (so their snapshot is complete).
func (r *Room) maybeCompact() {
	r.mu.Lock()
	if r.compacting {
		if time.Since(r.compactStart) > compactTimeout {
			r.compacting = false // stale request; allow a retry
		} else {
			r.mu.Unlock()
			return
		}
	}
	if len(r.updateLog) <= compactThreshold {
		r.mu.Unlock()
		return
	}
	var chosen *Client
	for c := range r.clients {
		if time.Since(c.connectedAt) >= compactMinAge && !c.lossy.Load() && !c.awaitingFull.Load() {
			chosen = c
			break
		}
	}
	if chosen == nil {
		r.mu.Unlock()
		return
	}
	r.compacting = true
	r.compactStart = time.Now()
	chosen.compactMark.Store(int64(len(r.updateLog)))
	chosen.awaitingFull.Store(true)
	r.mu.Unlock()

	// Ask the chosen client for its full document (empty state vector request).
	select {
	case chosen.send <- encodeSyncStep1():
	default:
		chosen.awaitingFull.Store(false)
		r.mu.Lock()
		r.compacting = false
		r.mu.Unlock()
	}
}

// persistRoom writes the room's update log to PostgreSQL as one blob.
func (h *Hub) persistRoom(r *Room) {
	r.mu.RLock()
	blob := encodeLog(r.updateLog)
	r.mu.RUnlock()
	if len(blob) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := models.SaveState(ctx, h.db, r.id, blob); err != nil {
		slog.Error("persist room state", "room", r.id, "err", err)
		return
	}
	slog.Info("persisted room state", "room", r.id, "bytes", len(blob))
}

// PersistAll snapshots every active room — used on graceful shutdown.
func (h *Hub) PersistAll() {
	h.mu.RLock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.RUnlock()
	for _, r := range rooms {
		h.persistRoom(r)
	}
}

// Handler returns the Fiber websocket.Handler for /ws/:roomId.
func (h *Hub) Handler() func(*websocket.Conn) {
	return func(conn *websocket.Conn) {
		// The websocket runs in its own goroutine, outside Fiber's recover
		// middleware — guard it so one bad frame can't crash the process.
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("websocket handler panic", "recover", rec)
				_ = conn.Close()
			}
		}()

		roomID := conn.Params("roomId")
		if _, err := uuid.Parse(roomID); err != nil {
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid room id"))
			return
		}

		room := h.openRoom(roomID)
		if room == nil {
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "unknown room"))
			return
		}

		// Reject if the room is already at its connection cap.
		room.mu.RLock()
		full := len(room.clients) >= maxClientsPerRoom
		room.mu.RUnlock()
		if full {
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "room is full"))
			return
		}

		conn.SetReadLimit(maxMessageBytes)
		client := &Client{
			conn:        conn,
			send:        make(chan []byte, 256),
			roomID:      roomID,
			userID:      uuid.NewString(),
			connectedAt: time.Now(),
		}
		room.register <- client

		// Pull any offline edits the client arrived with (its reply is appended,
		// not treated as authoritative). The client's own SyncStep1 triggers our
		// full-state reply (sendState) which flips it to `synced`.
		client.trySend(encodeSyncStep1())

		go client.writePump()
		client.readPump(room)
	}
}

// sendState replays the room's current update log to a single client: the base
// as SyncStep2 (which flips the client to `synced`) followed by each update.
func (c *Client) sendState(room *Room) {
	room.mu.RLock()
	log := make([][]byte, len(room.updateLog))
	copy(log, room.updateLog)
	room.mu.RUnlock()

	if len(log) == 0 {
		c.trySend(encodeSyncStep2(emptyDocUpdate))
		return
	}
	c.trySend(encodeSyncStep2(log[0]))
	for _, u := range log[1:] {
		c.trySend(encodeSyncUpdate(u))
	}
}

// readPump reads frames until the socket closes, driving the sync handshake,
// capturing updates/snapshots, and relaying every frame verbatim.
func (c *Client) readPump(room *Room) {
	defer func() {
		room.unregister <- c
		_ = c.conn.Close()
	}()

	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// Yjs frames are binary; ignore anything else (never JSON-decoded).
		if mt != websocket.BinaryMessage || len(data) == 0 {
			continue
		}

		// Inspect only the type prefix to drive the protocol; bytes are relayed
		// untouched.
		if syncType, payload, isSync := classifySync(data); isSync {
			switch syncType {
			case syncStep1:
				c.sendState(room) // peer asked for state → send full log
			case syncStep2:
				if c.awaitingFull.CompareAndSwap(true, false) {
					room.compactWith(c, payload) // authoritative full snapshot
				} else {
					room.appendUpdate(payload) // offline edits / peer diff
				}
			case syncUpdate:
				room.appendUpdate(payload)
			}
		}

		room.broadcast <- envelope{sender: c, data: data}
	}
}

// writePump drains the send channel and sends periodic pings (keepalive).
func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok { // room closed the channel
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// --- y-websocket / lib0 binary helpers -------------------------------------

func encodeSyncStep1() []byte {
	var b []byte
	b = writeVarUint(b, messageSync)
	b = writeVarUint(b, syncStep1)
	b = writeVarUint8Array(b, []byte{0x00}) // empty state vector → "send me everything"
	return b
}

func encodeSyncStep2(update []byte) []byte {
	var b []byte
	b = writeVarUint(b, messageSync)
	b = writeVarUint(b, syncStep2)
	b = writeVarUint8Array(b, update)
	return b
}

func encodeSyncUpdate(update []byte) []byte {
	var b []byte
	b = writeVarUint(b, messageSync)
	b = writeVarUint(b, syncUpdate)
	b = writeVarUint8Array(b, update)
	return b
}

// classifySync inspects a frame's type prefix. For messageSync frames it returns
// the sync sub-type and (for step1/step2/update) the length-prefixed payload.
func classifySync(data []byte) (syncType uint64, payload []byte, isSync bool) {
	msgType, rest, ok := readVarUint(data)
	if !ok || msgType != messageSync {
		return 0, nil, false
	}
	st, rest, ok := readVarUint(rest)
	if !ok {
		return 0, nil, false
	}
	pl, _, _ := readVarUint8Array(rest) // payload absent on malformed frames
	return st, pl, true
}

// encodeLog serializes an update log as a sequence of length-prefixed blobs.
func encodeLog(log [][]byte) []byte {
	var b []byte
	for _, u := range log {
		b = writeVarUint8Array(b, u)
	}
	return b
}

// decodeLog reverses encodeLog.
func decodeLog(blob []byte) [][]byte {
	var out [][]byte
	rest := blob
	for len(rest) > 0 {
		u, r, ok := readVarUint8Array(rest)
		if !ok {
			break
		}
		out = append(out, append([]byte(nil), u...))
		rest = r
	}
	return out
}

// writeVarUint appends n as a lib0 variable-length unsigned integer.
func writeVarUint(b []byte, n uint64) []byte {
	for n > 0x7f {
		b = append(b, byte(0x80|(n&0x7f)))
		n >>= 7
	}
	return append(b, byte(n&0x7f))
}

// writeVarUint8Array appends a length-prefixed byte slice.
func writeVarUint8Array(b, data []byte) []byte {
	b = writeVarUint(b, uint64(len(data)))
	return append(b, data...)
}

// readVarUint decodes a lib0 varuint, returning the value and the remainder.
func readVarUint(b []byte) (uint64, []byte, bool) {
	var n uint64
	var shift uint
	for i, c := range b {
		n |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return n, b[i+1:], true
		}
		shift += 7
		if shift > 63 {
			return 0, nil, false
		}
	}
	return 0, nil, false
}

// readVarUint8Array decodes a length-prefixed byte slice.
func readVarUint8Array(b []byte) ([]byte, []byte, bool) {
	length, rest, ok := readVarUint(b)
	if !ok || uint64(len(rest)) < length {
		return nil, nil, false
	}
	return rest[:length], rest[length:], true
}
