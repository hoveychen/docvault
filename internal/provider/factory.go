package provider

import (
	"fmt"
	"sort"
	"sync"
)

// ConnDef is a provider-agnostic connection definition handed to a Factory. It
// mirrors the columns of provider_connections: AppID/AppSecret are the OAuth
// client credential, and Domain carries a type-specific variant (feishu/lark for
// Feishu, the Entra tenant for Microsoft, unused for Google/Tencent).
type ConnDef struct {
	Type      string
	Key       string
	Label     string
	AppID     string
	AppSecret string
	Domain    string
}

// Factory builds a Provider from a ConnDef. Each provider package registers one
// from its init() via RegisterFactory.
type Factory func(ConnDef) (Provider, error)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]Factory{}
)

// RegisterFactory registers a provider factory under a type key (e.g. "feishu",
// "google"). Provider packages call this from init(). It panics on duplicate
// registration, which is always a programming error.
func RegisterFactory(providerType string, f Factory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	if _, dup := factories[providerType]; dup {
		panic("provider: duplicate factory registered for type " + providerType)
	}
	factories[providerType] = f
}

// Build constructs a Provider for def, dispatching on def.Type. It returns an
// error when no factory is registered for the type (e.g. the provider package
// was not imported for its side effects).
func Build(def ConnDef) (Provider, error) {
	factoriesMu.RLock()
	f, ok := factories[def.Type]
	factoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider: no factory registered for type %q", def.Type)
	}
	return f(def)
}

// HasFactory reports whether a factory is registered for the given provider type.
func HasFactory(providerType string) bool {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	_, ok := factories[providerType]
	return ok
}

// FactoryTypes lists the registered provider type keys, sorted. Used by the admin
// UI to offer the available connection types.
func FactoryTypes() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	out := make([]string, 0, len(factories))
	for t := range factories {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
