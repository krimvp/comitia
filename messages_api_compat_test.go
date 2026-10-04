package comitia

// The frozen pre-discovery message method sets are shared with the discovery
// compatibility tests, so new capabilities stay optional for both.
var _ MessageAPI = legacyMessageAPI(nil)
var _ MessageReadStore = legacyMessageReadStore(nil)
var _ MessageSchemaAPI = (*Service)(nil)
