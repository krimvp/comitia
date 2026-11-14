package service

import (
	"github.com/krimvp/comitia/internal/model"
)

// The frozen pre-discovery message method sets are shared with the discovery
// compatibility tests, so new capabilities stay optional for both.
var _ model.MessageAPI = legacyMessageAPI(nil)
var _ model.MessageReadStore = legacyMessageReadStore(nil)
var _ model.MessageSchemaAPI = (*Service)(nil)
