// Android emulator runtime placeholder for Cyborg.
// It preserves runtime naming while adb-backed control lives in the driver.
package androidemulator

import "github.com/williamfzc/cyborg/internal/runtime/qemu"

type Runtime struct {
	Name      string       `json:"name"`
	Image     string       `json:"image"`
	Transport string       `json:"transport"`
	Underlay  qemu.Runtime `json:"underlay"`
}

func Default() Runtime {
	return Runtime{
		Name:      "android-emulator",
		Image:     "android-default-avd",
		Transport: "adb",
		Underlay:  qemu.Default(),
	}
}
