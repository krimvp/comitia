package comitia

import (
	"github.com/krimvp/comitia/internal/model"
)

// Server information, storage status and the build stamp.

const ProtocolVersion = model.ProtocolVersion

// Version can be set with -ldflags '-X github.com/krimvp/comitia.Version=...'.
var Version string

// Revision and Dirty may be stamped by source installers even with buildvcs=false.
var Revision, Dirty string

// The root variables are the build stamp's single source of truth, set with
// -ldflags -X (in place before any init) or assigned at runtime. The
// implementation reads them at call time through this reader.
func init() {
	model.BuildStamp = func() (string, string, string) { return Version, Revision, Dirty }
}

type BuildInfo = model.BuildInfo

func BuildIdentity() BuildInfo {
	return model.BuildIdentity()
}

func BuildVersion() string {
	return model.BuildVersion()
}

type ServerInfo = model.ServerInfo

// Features returns the feature inventory compiled into this binary.
func Features() []string {
	return model.Features()
}

type ServerInfoAPI = model.ServerInfoAPI

const StorageProbeTimeout = model.StorageProbeTimeout

type StorageStatus = model.StorageStatus

type StorageStatusAPI = model.StorageStatusAPI
