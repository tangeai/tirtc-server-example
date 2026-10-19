package ai

import (
	"context"
	"sync"
	"time"
)

// PluginNames reads display names without granting access to plugin configuration.
// Implementations must respect context cancellation.
type PluginNames interface {
	DevicePluginName(context.Context, string) (string, error)
}

// PluginRef is read-only display metadata for an existing role association.
type PluginRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResolvePluginNames enriches already-authorized role references. It preserves
// their order, deduplicates IDs and retains unresolved references. A bounded
// lookup budget keeps optional display metadata from blocking the role page.
func ResolvePluginNames(ctx context.Context, source PluginNames, ids []string) []PluginRef {
	refs := make([]PluginRef, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			refs = append(refs, PluginRef{ID: id})
		}
	}
	if len(refs) == 0 {
		return refs
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	jobs := make(chan int, len(refs))
	for i := range refs {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for i := 0; i < 4 && i < len(refs); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				name, err := source.DevicePluginName(ctx, refs[index].ID)
				if err == nil {
					refs[index].Name = name
				}
			}
		}()
	}
	workers.Wait()
	return refs
}
