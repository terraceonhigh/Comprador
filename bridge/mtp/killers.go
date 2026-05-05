package mtp

import (
	"log"
	"os/exec"
)

// competingProcesses are macOS daemons that auto-claim USB MTP/PTP interfaces.
// They are launchd-managed and respawn within ~60ms after killall, so the
// kill must happen as close to LIBMTP_Open as possible.
var competingProcesses = []string{
	"ptpcamerad",
	"PTPCamera",
	"AMPDeviceDiscoveryAgent",
	"AMPDevicesAgent",
	"MTPCamera",
}

// killCompetingProcesses sends SIGKILL to known USB-claiming daemons.
// This is best-effort: launchd will respawn them, so call it immediately
// before attempting libusb_claim_interface.
func killCompetingProcesses() {
	for _, name := range competingProcesses {
		cmd := exec.Command("/usr/bin/killall", "-9", name)
		if err := cmd.Run(); err == nil {
			log.Printf("Killed %s", name)
		}
	}
}
