package cachex

import "github.com/redis/go-redis/v9"

// setLua atomically increments the envelope version, writes the new header and data, and
// applies the TTL. It touches exactly one key, so it is safe in a Redis Cluster.
// | setLua 原子递增信封版本、写入新头与数据并施加 TTL。它只操作一个键，因此在 Redis Cluster 中安全。
const setLua = `
local cur = redis.call('GET', KEYS[1])
local v = 0
if cur and string.len(cur) >= 11 and string.sub(cur, 1, 2) == 'YD' then
  for i = 4, 11 do
    v = v * 256 + string.byte(cur, i)
  end
end
local nv = v + 1
local x = nv
local bytes = {}
for i = 1, 8 do
  bytes[9 - i] = string.char(x % 256)
  x = math.floor(x / 256)
end
local payload = 'YD' .. string.char(tonumber(ARGV[2])) .. table.concat(bytes) .. ARGV[1]
local ttl = tonumber(ARGV[3])
if ttl and ttl > 0 then
  redis.call('SET', KEYS[1], payload, 'PX', ttl)
else
  redis.call('SET', KEYS[1], payload)
end
return nv
`

// casLua writes only when the current envelope version equals the expected version. It
// returns the new version, or -1 when the write lost against a concurrent mutation.
// | casLua 仅在当前信封版本等于期望版本时写入。成功返回新版本，输给并发变更时返回 -1。
const casLua = `
local cur = redis.call('GET', KEYS[1])
local v = 0
local found = 0
if cur and string.len(cur) >= 11 and string.sub(cur, 1, 2) == 'YD' then
  found = 1
  for i = 4, 11 do
    v = v * 256 + string.byte(cur, i)
  end
end
local expect = tonumber(ARGV[4])
if (found == 0 and expect ~= 0) or (found == 1 and v ~= expect) then
  return -1
end
local nv = v + 1
local x = nv
local bytes = {}
for i = 1, 8 do
  bytes[9 - i] = string.char(x % 256)
  x = math.floor(x / 256)
end
local payload = 'YD' .. string.char(tonumber(ARGV[2])) .. table.concat(bytes) .. ARGV[1]
local ttl = tonumber(ARGV[3])
if ttl and ttl > 0 then
  redis.call('SET', KEYS[1], payload, 'PX', ttl)
else
  redis.call('SET', KEYS[1], payload)
end
return nv
`

// Redis scripts are compiled once and executed by SHA with an EVAL fallback. | Redis 脚本编译一次，按 SHA 执行并在需要时回退到 EVAL。
var (
	setScript = redis.NewScript(setLua)
	casScript = redis.NewScript(casLua)
)
