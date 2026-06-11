package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Room mirrors the `rooms` table. yjs_state holds a Y.encodeStateAsUpdate
// snapshot and may be nil for a freshly created room.
type Room struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	YjsState  []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoomSummary is the lightweight shape returned by the list endpoint.
type RoomSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListRooms returns all rooms ordered by most recently updated.
func ListRooms(ctx context.Context, db *sql.DB) ([]RoomSummary, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title, updated_at FROM rooms ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	defer rows.Close()

	out := make([]RoomSummary, 0)
	for rows.Next() {
		var r RoomSummary
		if err := rows.Scan(&r.ID, &r.Title, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan room: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRoom inserts a new room and returns the generated row.
func CreateRoom(ctx context.Context, db *sql.DB, title string) (*Room, error) {
	var r Room
	err := db.QueryRowContext(ctx,
		`INSERT INTO rooms (title) VALUES ($1)
		 RETURNING id, title, created_at, updated_at`, title).
		Scan(&r.ID, &r.Title, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create room: %w", err)
	}
	return &r, nil
}

// GetRoom fetches a single room including its persisted Yjs state.
func GetRoom(ctx context.Context, db *sql.DB, id string) (*Room, error) {
	var r Room
	err := db.QueryRowContext(ctx,
		`SELECT id, title, yjs_state, created_at, updated_at
		 FROM rooms WHERE id = $1`, id).
		Scan(&r.ID, &r.Title, &r.YjsState, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get room: %w", err)
	}
	return &r, nil
}

// DeleteRoom removes a room. Returns false if no row matched.
func DeleteRoom(ctx context.Context, db *sql.DB, id string) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM rooms WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete room: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateRoomTitle renames a room and bumps updated_at. Returns nil if no
// room with that id exists.
func UpdateRoomTitle(ctx context.Context, db *sql.DB, id, title string) (*RoomSummary, error) {
	var r RoomSummary
	err := db.QueryRowContext(ctx,
		`UPDATE rooms SET title = $1, updated_at = NOW() WHERE id = $2
		 RETURNING id, title, updated_at`, title, id).
		Scan(&r.ID, &r.Title, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update room title: %w", err)
	}
	return &r, nil
}

// SaveState persists a Yjs snapshot and bumps updated_at. Called when the last
// client leaves a room and on graceful shutdown.
func SaveState(ctx context.Context, db *sql.DB, id string, state []byte) error {
	_, err := db.ExecContext(ctx,
		`UPDATE rooms SET yjs_state = $1, updated_at = NOW() WHERE id = $2`,
		state, id)
	if err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
