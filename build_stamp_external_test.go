package comitia_test

import (
	"context"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

// The public build stamp variables are read at call time, so a program that
// embeds comitia may assign them at runtime, for example in main.
func TestBuildStampAssignedAtRuntime(t *testing.T) {
	oldVersion, oldRevision, oldDirty := comitia.Version, comitia.Revision, comitia.Dirty
	t.Cleanup(func() { comitia.Version, comitia.Revision, comitia.Dirty = oldVersion, oldRevision, oldDirty })
	comitia.Version, comitia.Revision, comitia.Dirty = "v7.7.7", "rev-set-at-runtime", "false"
	want := comitia.BuildInfo{Version: "v7.7.7", Revision: "rev-set-at-runtime", Dirty: "false"}

	if got := comitia.BuildVersion(); got != want.Version {
		t.Errorf("BuildVersion()=%q want %q", got, want.Version)
	}
	if got := comitia.BuildIdentity(); got != want {
		t.Errorf("BuildIdentity()=%+v want %+v", got, want)
	}

	ctx := context.Background()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "test", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.ServerInfo(ctx, admin.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != want.Version || info.Build != want {
		t.Errorf("ServerInfo version=%q build=%+v want %q %+v", info.Version, info.Build, want.Version, want)
	}

	// Assigning again takes effect again: nothing caches the first values.
	comitia.Version, comitia.Revision, comitia.Dirty = "", "", "maybe"
	if got := comitia.BuildIdentity(); got.Version == "v7.7.7" || got.Revision == "rev-set-at-runtime" || got.Dirty != "unknown" {
		t.Errorf("cleared stamp still reported: %+v", got)
	}
}
