// Package httpapi exposes comitia.API over a small, strict JSON protocol.
//
// It has two roles that share the wire types in wire*.go:
//
//   - Server (server*.go): NewHandler serves each operation as POST
//     /v1/<route> with a bearer token and a JSON body of at most 1 MiB, plus
//     the inspector UI (inspector.go) under /ui/. Each server_<domain>.go
//     returns one route group; handler.endpoints in server.go joins them into
//     the route table. Bootstrap is deliberately not routed.
//   - Client (client*.go): Client is a remote comitia.API over those routes.
//     It negotiates the canonical wire with the X-Comitia-Wire header
//     (without it the server answers with the original Go field names) and
//     reads at most 16 MiB per response. Against an older server it falls
//     back to unpaged lists, and reports a newer request member the server
//     rejects as an unsupported error rather than retrying without it.
//
// Errors (errors.go): a failed call answers comitia.ErrorInfo JSON whose code
// sets the status: invalid 400, unauthenticated 401, forbidden 403,
// not_found 404, conflict, stale_claim and bootstrap_used 409,
// deadline_exceeded and cancelled 503, and any other failure 500 "internal
// server error"; a method other than POST answers 405 method_not_allowed.
// The Client maps a code back to its comitia sentinel error inside a
// comitia.OperationalError; other codes become a "remote error".
package httpapi
