package model

import (
	"time"

	"github.com/google/uuid"
)

type Artist struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
