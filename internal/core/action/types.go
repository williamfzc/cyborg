// Action model shared across CLI, daemon, and drivers.
// It is the protocol payload for device operations.
package action

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Artifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type Action struct {
	DeviceID  string         `json:"device_id"`
	Name      string         `json:"name"`
	Params    map[string]any `json:"params,omitempty"`
	TimeoutMs int            `json:"timeout_ms,omitempty"`
}

type Result struct {
	OK        bool       `json:"ok"`
	Result    any        `json:"result,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Error     *Error     `json:"error,omitempty"`
}
