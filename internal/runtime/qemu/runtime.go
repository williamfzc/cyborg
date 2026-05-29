// QEMU runtime descriptor for Cyborg.
// It records low-level runtime naming without exposing it as the main product abstraction.
package qemu

import "runtime"

type Runtime struct {
	Provider     string `json:"provider"`
	Acceleration string `json:"acceleration"`
	Guest        string `json:"guest"`
}

func Default() Runtime {
	acceleration := "tcg"
	if runtime.GOOS == "darwin" {
		acceleration = "hvf"
	}
	if runtime.GOOS == "linux" {
		acceleration = "kvm"
	}

	return Runtime{
		Provider:     "qemu",
		Acceleration: acceleration,
		Guest:        "android",
	}
}
