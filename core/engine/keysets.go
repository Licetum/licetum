package engine

// keySets returns the key sets of the identity providers of this process. The settings come from the configuration
// of the App the first time they are needed, so an App built by hand (without New) has the defaults.
func (a *App) keySets() *KeySets {
	a.jwksOnce.Do(func() {
		t := a.tuningOrDefault()
		a.jwks.HTTP = a.HTTP
		a.jwks.TTL = t.jwksTTL
		a.jwks.MinRefresh = t.jwksMinRefresh
		a.jwks.Observe = a.observeJWKS
	})
	return &a.jwks
}

// observeJWKS counts a fetch of a key set by its result.
func (a *App) observeJWKS(result string) {
	if a.metrics != nil {
		a.metrics.jwksFetches.WithLabelValues(result).Inc()
	}
}
