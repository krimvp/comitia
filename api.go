package comitia

import (
	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/service"
)

// The application API, its configuration and the service that implements it.

// API is the library-first application surface.
type API = model.API

type Clock = model.Clock

type Config = model.Config

type Service = service.Service

func New(cfg Config) (*Service, error) {
	return service.New(cfg)
}
