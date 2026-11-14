// Package service owns *Service, the implementation of the application API:
// authentication and authorization, context and grant rules, message
// delivery, task leases and presence, over any Store. Files are prefixed by
// domain: sessions, grants, contexts, messages, tasks, presence, pages and
// serverinfo. It imports internal/model and internal/schema only.
package service
