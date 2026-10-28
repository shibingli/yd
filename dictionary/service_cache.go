// This file owns dictionary resolution and cache invalidation: the tenant-over-system fallback,
// the per-scope cache keys and the negative-cache behaviour. | 本文件负责字典解析与缓存失效：
// 租户优先/系统兜底、按范围的缓存键以及负缓存行为。

package dictionary

import (
	"context"
	"errors"
	"strings"

	"github.com/shibingli/ydx/cache"
	ydlog "github.com/shibingli/ydx/log"
)

// Resolve returns one enabled dictionary by code. A tenant dictionary wins over a system dictionary
// with the same code; a tenant without its own entry falls back to the system layer. |
// Resolve 按编码返回一个启用字典。同一编码的租户字典优先于系统字典；租户没有自己的条目时
// 回退到系统层。
func (s *Service) Resolve(ctx context.Context, tenantID, code string) (Snapshot, error) {
	tenantID = strings.TrimSpace(tenantID)
	code = strings.ToLower(strings.TrimSpace(code))
	if tenantID == "" || !codeRe.MatchString(code) {
		return Snapshot{}, ErrInvalidInput
	}
	snapshot, err := s.resolveScope(ctx, tenantID, code)
	if err == nil || !errors.Is(err, ErrNotFound) || tenantID == SystemTenantID {
		return snapshot, err
	}
	return s.resolveScope(ctx, SystemTenantID, code)
}

// resolveScope reads one scope without falling back. | resolveScope 读取一个范围，不执行回退。
func (s *Service) resolveScope(ctx context.Context, tenantID, code string) (Snapshot, error) {
	if s.snapshots == nil {
		return s.resolveUncached(ctx, tenantID, code)
	}
	snapshot, err := s.snapshots.GetOrLoad(ctx, cacheKey(tenantID, code), func(ctx context.Context) (Snapshot, error) {
		loaded, loadErr := s.store.Resolve(ctx, tenantID, []string{code})
		if loadErr != nil {
			return Snapshot{}, loadErr
		}
		if value, ok := loaded[code]; ok {
			if value.Scope == "" {
				value.Scope = ScopeOf(tenantID)
			}
			return value, nil
		}
		return Snapshot{}, cache.ErrNotFound
	})
	if err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return Snapshot{}, ErrNotFound
		}
		if errors.Is(err, ErrUnavailable) {
			return Snapshot{}, err
		}
		// A cache failure (for example an entry larger than the L1 shard) must not fail the read;
		// the store is the source of truth, so fall back to it. | 缓存故障（例如条目超过 L1 分片）
		// 不得让读取失败；存储才是事实来源，因此回退到存储。
		s.log.Warn("dictionary cache read failed, reading the store instead | 数据字典缓存读取失败，改为读存储",
			ydlog.Err(err),
			ydlog.Str("code", code),
		)
		return s.resolveUncached(ctx, tenantID, code)
	}
	if snapshot.Code == "" {
		return Snapshot{}, ErrNotFound
	}
	if snapshot.Scope == "" {
		snapshot.Scope = ScopeOf(tenantID)
	}
	return snapshot, nil
}

// ResolveMany returns every enabled dictionary named by codes, applying the same tenant-over-system
// precedence as Resolve. A code without an enabled type in either layer is absent from the result.
// Warm codes are answered from the cache; a cold code is loaded through GetOrLoad, whose
// single-flight CAS write-back never overwrites a concurrent write. |
// ResolveMany 返回 codes 命中的全部启用字典，并采用与 Resolve 相同的「租户优先、系统兜底」规则；
// 两层都没有启用类型的编码不出现在结果中。命中的编码由缓存直接答复；冷编码经 GetOrLoad 加载，
// 其单飞 CAS 回填不会覆盖并发写入。
func (s *Service) ResolveMany(ctx context.Context, tenantID string, codes []string) (map[string]Snapshot, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, ErrInvalidInput
	}
	normalized, err := s.normalizeCodes(codes)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return map[string]Snapshot{}, nil
	}
	if s.snapshots == nil {
		result, err := s.store.Resolve(ctx, tenantID, normalized)
		if err != nil {
			return nil, err
		}
		if result == nil {
			result = make(map[string]Snapshot, len(normalized))
		}
		for code, snapshot := range result {
			if snapshot.Scope == "" {
				snapshot.Scope = ScopeOf(tenantID)
				result[code] = snapshot
			}
		}
		if tenantID != SystemTenantID {
			missing := make([]string, 0, len(normalized))
			for _, code := range normalized {
				if _, ok := result[code]; !ok {
					missing = append(missing, code)
				}
			}
			if len(missing) > 0 {
				systemSnapshots, err := s.store.Resolve(ctx, SystemTenantID, missing)
				if err != nil {
					return nil, err
				}
				for code, snapshot := range systemSnapshots {
					if snapshot.Scope == "" {
						snapshot.Scope = ScopeSystem
					}
					result[code] = snapshot
				}
			}
		}
		return result, nil
	}
	result := make(map[string]Snapshot, len(normalized))
	for _, code := range normalized {
		snapshot, resolveErr := s.Resolve(ctx, tenantID, code)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrNotFound) {
				continue
			}
			return nil, resolveErr
		}
		result[code] = snapshot
	}
	return result, nil
}

// resolveUncached reads one snapshot directly from the store. | resolveUncached 直接从存储读取一个快照。
func (s *Service) resolveUncached(ctx context.Context, tenantID, code string) (Snapshot, error) {
	loaded, err := s.store.Resolve(ctx, tenantID, []string{code})
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot, ok := loaded[code]; ok {
		if snapshot.Scope == "" {
			snapshot.Scope = ScopeOf(tenantID)
		}
		return snapshot, nil
	}
	return Snapshot{}, ErrNotFound
}

// Invalidate removes one dictionary from both cache levels. External writers that update the tables
// directly must call it; the service does so after every write of its own. | Invalidate 从两级缓存中移除
// 一个字典。直接写表的调用方必须调用它；服务自身在每次写入后都会调用。
func (s *Service) Invalidate(ctx context.Context, tenantID, code string) error {
	if s.snapshots == nil {
		return nil
	}
	return s.snapshots.Delete(ctx, cacheKey(tenantID, code))
}

// invalidate removes one dictionary from the cache and logs a failure without failing the write. |
// invalidate 从缓存移除一个字典；失效失败只记录日志，不让写入失败。
func (s *Service) invalidate(ctx context.Context, tenantID, code string) {
	if err := s.Invalidate(ctx, tenantID, code); err != nil {
		s.log.Warn("dictionary cache invalidation failed | 数据字典缓存失效失败",
			ydlog.Err(err),
			ydlog.Str("code", code),
		)
	}
}

// cacheKey builds the cache key of one dictionary. The scope prefix keeps the system layer separate
// from every tenant, and the separator cannot appear in a dictionary code. |
// cacheKey 构造一个字典的缓存键。范围前缀把系统层与每个租户分开，分隔符不会出现在字典编码里。
func cacheKey(tenantID, code string) string {
	if tenantID == SystemTenantID {
		return "system|" + code
	}
	return "tenant:" + tenantID + "|" + code
}
