// Package model holds Comitia's data types, errors, permissions, the API and
// Store interfaces, and the rules shared by the service and the stores. It
// imports no other package of the module.
//
// Package comitia (pkg/comitia) aliases these types, so their exported methods and fields
// are public API. Nothing here may gain an exported method or field unless it
// is meant to become public API through those aliases; a helper that another
// internal package needs is an exported free function instead.
package model
