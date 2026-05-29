// Session model for Cyborg device usage records.
// It represents attachment metadata separate from the device itself.
package session

import "time"

type Session struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
	Attached  bool      `json:"attached"`
}
