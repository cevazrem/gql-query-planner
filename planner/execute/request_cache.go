package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
)

type cacheEntry struct {
	ready chan struct{}
	val   any
	err   error
}

type cacheLookupState uint8

const (
	cacheLookupDisabled cacheLookupState = iota
	cacheLookupMissOwner
	cacheLookupHitReady
	cacheLookupHitPending
)

type cacheLookupResult struct {
	state cacheLookupState
	entry *cacheEntry
}

type requestCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

func newRequestCache() *requestCache {
	return &requestCache{entries: make(map[string]*cacheEntry)}
}

func (c *requestCache) lookupOrReserve(key string) cacheLookupResult {
	if c == nil || key == "" {
		return cacheLookupResult{state: cacheLookupDisabled}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok {
		select {
		case <-e.ready:
			return cacheLookupResult{state: cacheLookupHitReady, entry: e}
		default:
			return cacheLookupResult{state: cacheLookupHitPending, entry: e}
		}
	}

	e := &cacheEntry{ready: make(chan struct{})}
	c.entries[key] = e
	return cacheLookupResult{state: cacheLookupMissOwner, entry: e}
}

func (c *requestCache) complete(key string, val any, err error) {
	if c == nil || key == "" {
		return
	}

	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return
	}
	select {
	case <-e.ready:
		c.mu.Unlock()
		return
	default:
	}
	e.val = val
	e.err = err
	close(e.ready)
	c.mu.Unlock()
}

func (e *cacheEntry) wait(ctx context.Context) (any, error) {
	if e == nil {
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.ready:
		return e.val, e.err
	}
}

func shouldUseNodeCache(node *logical.Node, strat physical.NodeStrategy) bool {
	return node != nil && node.Meta.Caps.Cacheable && strat.UseCache
}

func cacheKeyForNode(node *logical.Node, parent any) string {
	if node == nil || !node.Meta.Caps.Cacheable {
		return ""
	}

	parentKey, ok := cacheParentKey(parent, node.Meta.Caps.CacheKey, node.Meta.Caps.BatchKey)
	if !ok {
		return ""
	}

	argsKey, ok := stableArgsKey(node.Args)
	if !ok {
		return ""
	}

	return strings.Join([]string{
		node.ResolverKey(),
		parentKey,
		argsKey,
		selectedChildrenShape(node),
	}, "|")
}

func stableArgsKey(args map[string]any) (string, bool) {
	if len(args) == 0 {
		return "-", true
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func selectedChildrenShape(node *logical.Node) string {
	if node == nil || len(node.Children) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(node.Children))
	for _, ch := range node.Children {
		keys = append(keys, responseKey(ch))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func cacheParentKey(parent any, hints ...string) (string, bool) {
	if parent == nil {
		return "__root__", true
	}

	for _, hint := range hints {
		if s, ok := tryExtractParentKey(parent, hint); ok {
			return s, true
		}
	}
	if s, ok := tryExtractParentKey(parent, "id"); ok {
		return s, true
	}

	rv := reflect.ValueOf(parent)
	if rv.IsValid() && rv.Kind() == reflect.Pointer && !rv.IsNil() {
		return fmt.Sprintf("ptr:%T:%p", parent, parent), true
	}

	return fmt.Sprintf("val:%T:%v", parent, parent), true
}

func tryExtractParentKey(parent any, hint string) (string, bool) {
	if parent == nil || hint == "" {
		return "", false
	}
	rv := reflect.ValueOf(parent)
	if !rv.IsValid() {
		return "", false
	}

	candidates := candidateNames(hint)
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		for _, name := range candidates {
			m := rv.MethodByName(name)
			if val, ok := callZeroArgStringer(m); ok {
				return val, true
			}
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return "", false
	}

	for _, name := range candidates {
		f := rv.FieldByName(name)
		if val, ok := valueToString(f); ok {
			return val, true
		}
	}
	return "", false
}

func candidateNames(hint string) []string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return nil
	}
	parts := strings.FieldsFunc(hint, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.'
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	camel := strings.Join(parts, "")
	if camel == "" {
		return nil
	}
	out := []string{camel, "Get" + camel}
	if strings.HasSuffix(camel, "Id") {
		stem := strings.TrimSuffix(camel, "Id")
		out = append(out, stem+"ID", "Get"+stem+"ID")
	}
	return dedupStrings(out)
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func callZeroArgStringer(m reflect.Value) (string, bool) {
	if !m.IsValid() || m.Kind() != reflect.Func || m.Type().NumIn() != 0 {
		return "", false
	}
	outs := m.Call(nil)
	if len(outs) == 0 || len(outs) > 2 {
		return "", false
	}
	if len(outs) == 2 && !outs[1].IsNil() {
		return "", false
	}
	return valueToString(outs[0])
}

func valueToString(v reflect.Value) (string, bool) {
	if !v.IsValid() {
		return "", false
	}
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return "", false
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Bool:
		if v.Bool() {
			return "true", true
		}
		return "false", true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprintf("%d", v.Uint()), true
	case reflect.Array, reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes()), true
		}
	}
	if v.CanInterface() {
		if s, ok := v.Interface().(fmt.Stringer); ok {
			return s.String(), true
		}
		return fmt.Sprintf("%v", v.Interface()), true
	}
	return "", false
}
