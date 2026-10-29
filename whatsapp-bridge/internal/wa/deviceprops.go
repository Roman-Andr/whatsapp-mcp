package wa

import (
	"errors"

	wastore "go.mau.fi/whatsmeow/store"
)

// ApplyDeviceProps sets what the phone learns about this device when it is
// linked:
//
//   - historyDays (WHATSAPP_HISTORY_DAYS) asks for that many days of history
//     instead of the roughly one year the phone sends by default; zero leaves
//     whatsmeow's default;
//   - deviceName (WHATSAPP_DEVICE_NAME) is the name shown under Linked
//     devices; empty leaves whatsmeow's default.
//
// Both only matter for a new pairing: an already linked device keeps what it
// got. whatsmeow takes these from its package-level store.DeviceProps and
// offers no per-client setting, so this changes a global; call it once,
// before the client pairs.
func ApplyDeviceProps(historyDays int, deviceName string) error {
	if historyDays != 0 {
		wastore.DeviceProps.RequireFullSync = new(true)
		if wastore.DeviceProps.GetHistorySyncConfig() == nil {
			return errors.New("whatsmeow has no history sync config to change")
		}
		wastore.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = new(uint32(historyDays)) //nolint:gosec // config checked days is non-negative
	}
	if deviceName != "" {
		wastore.DeviceProps.Os = new(deviceName)
	}
	return nil
}
