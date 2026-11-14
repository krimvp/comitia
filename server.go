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

// The build stamp is set on the root variables with -ldflags -X and copied
// into the implementation before any caller runs.
func init() {
	model.Version = Version
	model.Revision = Revision
	model.Dirty = Dirty
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
