// Package service is the business logic of the Licetum admin API: the management of services, resource types, groups,
// roles and access rules, the history of the changes, and the tools of the policy. It builds on the engine of the core
// module, which decides and synchronizes; the admin application embeds it.
package service

import (
	"github.com/licetum/licetum/core/engine"
)

// App is the admin application: the engine of the core with the writes and the tools of the management on top.
type App struct {
	*engine.App
}

// New wraps an engine.
func New(core *engine.App) *App { return &App{App: core} }
