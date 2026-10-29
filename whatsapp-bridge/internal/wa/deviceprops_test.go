package wa

import (
	"testing"

	wastore "go.mau.fi/whatsmeow/store"
)

// The device properties are a whatsmeow global, so these tests do not run
// in parallel and restore what they change.

func TestHistoryDaysConfigureFullSync(t *testing.T) {
	saved := wastore.DeviceProps.GetHistorySyncConfig().GetFullSyncDaysLimit()
	savedFull := wastore.DeviceProps.GetRequireFullSync()
	t.Cleanup(func() {
		wastore.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = new(saved)
		wastore.DeviceProps.RequireFullSync = new(savedFull)
	})
	if err := ApplyDeviceProps(0, ""); err != nil || wastore.DeviceProps.GetRequireFullSync() != savedFull {
		t.Fatalf("zero days changed the props: %v", err)
	}
	if err := ApplyDeviceProps(3650, ""); err != nil {
		t.Fatal(err)
	}
	if got := wastore.DeviceProps.GetHistorySyncConfig().GetFullSyncDaysLimit(); got != 3650 || !wastore.DeviceProps.GetRequireFullSync() {
		t.Errorf("days limit %d, full sync %v", got, wastore.DeviceProps.GetRequireFullSync())
	}
}

func TestDeviceNameSetsLinkedDeviceLabel(t *testing.T) {
	saved := wastore.DeviceProps.GetOs()
	t.Cleanup(func() { wastore.DeviceProps.Os = new(saved) })
	cases := []struct{ name, set, want string }{
		{"empty keeps the default", "", saved},
		{"name shown on the phone", "Snow ops bridge", "Snow ops bridge"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ApplyDeviceProps(0, c.set); err != nil {
				t.Fatal(err)
			}
			if got := wastore.DeviceProps.GetOs(); got != c.want {
				t.Errorf("Os %q, want %q", got, c.want)
			}
		})
	}
}
