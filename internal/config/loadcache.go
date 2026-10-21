package config

import (
	"reflect"
	"sync"
)

// loaded is the last config each path decoded to. The board answers every
// extension's per-session lookup with a fresh LoadDir, and decoding the file
// over the distribution defaults costs far more than the lookup it serves.
// The key is the file's bytes, not its mtime, so an edit inside one clock
// tick is still seen, and the defaults it was laid over, since UseDefaults
// can change them mid-process.
var loaded loadCache

type loadKey struct {
	path     string
	sum      [32]byte
	defaults uintptr
}

type loadCache struct {
	mu      sync.Mutex
	entries map[string]loadEntry
}

type loadEntry struct {
	key loadKey
	cfg Config
}

// get hands back a copy, so a caller that edits its Config's maps or slices
// cannot reach the cached one or another caller's.
func (c *loadCache) get(key loadKey) (Config, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key.path]
	if !ok || entry.key != key {
		return Config{}, false
	}
	return cloneConfig(entry.cfg), true
}

func (c *loadCache) put(key loadKey, cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]loadEntry{}
	}
	c.entries[key.path] = loadEntry{key: key, cfg: cloneConfig(cfg)}
}

func defaultsIdentity() uintptr {
	table := currentDefaults()
	if table == nil {
		return 0
	}
	return uintptr(reflect.ValueOf(table).UnsafePointer())
}

func cloneConfig(cfg Config) Config {
	return deepCopy(reflect.ValueOf(cfg)).Interface().(Config)
}

// deepCopy walks by reflection rather than by hand so a field added to Config
// later is copied without anyone remembering to.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(deepCopy(v.Field(i)))
			}
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	}
	return v
}
