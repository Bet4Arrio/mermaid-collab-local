package handlers

import (
	"database/sql"
	"encoding/base64"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"mermaid-collab/models"
)

// maxTitleLen bounds room titles to keep payloads small and avoid abuse.
const maxTitleLen = 200

// RoomsHandler bundles the DB dependency for the REST endpoints.
type RoomsHandler struct {
	DB *sql.DB
}

// NewRoomsHandler constructs a RoomsHandler.
func NewRoomsHandler(db *sql.DB) *RoomsHandler {
	return &RoomsHandler{DB: db}
}

// Register wires the room routes onto the given /api router group.
func (h *RoomsHandler) Register(api fiber.Router) {
	api.Get("/rooms", h.list)
	api.Post("/rooms", h.create)
	api.Get("/rooms/:id", h.get)
	api.Patch("/rooms/:id", h.rename)
	api.Delete("/rooms/:id", h.delete)
}

// GET /api/rooms
func (h *RoomsHandler) list(c *fiber.Ctx) error {
	rooms, err := models.ListRooms(c.Context(), h.DB)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(rooms)
}

type createRoomReq struct {
	Title string `json:"title"`
}

// POST /api/rooms
func (h *RoomsHandler) create(c *fiber.Ctx) error {
	var req createRoomReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid body")
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		return fiber.NewError(fiber.StatusBadRequest, "title is required")
	}
	if len(req.Title) > maxTitleLen {
		return fiber.NewError(fiber.StatusBadRequest, "title too long")
	}

	room, err := models.CreateRoom(c.Context(), h.DB, req.Title)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(room)
}

// GET /api/rooms/:id — returns title plus the persisted Yjs state as base64.
func (h *RoomsHandler) get(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid room id")
	}
	room, err := models.GetRoom(c.Context(), h.DB, id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if room == nil {
		return fiber.NewError(fiber.StatusNotFound, "room not found")
	}

	state := ""
	if len(room.YjsState) > 0 {
		state = base64.StdEncoding.EncodeToString(room.YjsState)
	}
	return c.JSON(fiber.Map{
		"id":         room.ID,
		"title":      room.Title,
		"yjs_state":  state,
		"created_at": room.CreatedAt,
		"updated_at": room.UpdatedAt,
	})
}

type renameRoomReq struct {
	Title string `json:"title"`
}

// PATCH /api/rooms/:id
func (h *RoomsHandler) rename(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid room id")
	}
	var req renameRoomReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid body")
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		return fiber.NewError(fiber.StatusBadRequest, "title is required")
	}
	if len(req.Title) > maxTitleLen {
		return fiber.NewError(fiber.StatusBadRequest, "title too long")
	}

	room, err := models.UpdateRoomTitle(c.Context(), h.DB, id, req.Title)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if room == nil {
		return fiber.NewError(fiber.StatusNotFound, "room not found")
	}
	return c.JSON(room)
}

// DELETE /api/rooms/:id
func (h *RoomsHandler) delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid room id")
	}
	ok, err := models.DeleteRoom(c.Context(), h.DB, id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "room not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
