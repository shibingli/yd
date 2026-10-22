package gormcache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/shibingli/yd/core/jsonx"
)

// maxKeyInput bounds the bytes hashed for one query key. | maxKeyInput 限制单次查询键参与哈希的字节数。
const maxKeyInput = 1 << 20

// keyBufPool reuses hash input buffers. | keyBufPool 复用哈希输入缓冲区。
var keyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
}

// queryKey builds a stable, cross-node key from the table, generations, destination type,
// SQL and every bind variable. It returns false when the input is too large or a variable
// cannot be serialized.
// | queryKey 由表名、代际、目标类型、SQL 与全部绑定变量构造跨节点稳定的键。输入过大或变量无法序列化时返回 false。
func (p *Plugin) queryKey(db *gorm.DB) (string, bool) {
	st := db.Statement
	if st == nil || st.Dest == nil || st.SQL.Len() == 0 {
		return "", false
	}
	table := p.tableOf(db)
	if table == "" {
		return "", false
	}
	epoch, generation := p.generations(table)
	bp := keyBufPool.Get().(*[]byte)
	buf := (*bp)[:0]
	release := func() {
		if cap(buf) <= 64<<10 {
			*bp = buf[:0]
			keyBufPool.Put(bp)
		}
	}
	buf = append(buf, table...)
	buf = append(buf, 0)
	buf = strconv.AppendUint(buf, epoch, 10)
	buf = append(buf, 0)
	buf = strconv.AppendUint(buf, generation, 10)
	buf = append(buf, 0)
	buf = append(buf, reflect.TypeOf(st.Dest).String()...)
	buf = append(buf, 0)
	buf = append(buf, st.SQL.String()...)
	for _, v := range st.Vars {
		raw, err := jsonx.Marshal(v)
		if err != nil {
			release()
			return "", false
		}
		if len(buf)+len(raw)+4 > maxKeyInput {
			release()
			return "", false
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(raw)))
		buf = append(buf, length[:]...)
		buf = append(buf, raw...)
	}
	if len(buf) > maxKeyInput {
		release()
		return "", false
	}
	sum := sha256.Sum256(buf)
	key := table + ":" + strconv.FormatUint(epoch, 10) + ":" + strconv.FormatUint(generation, 10) + ":" + hex.EncodeToString(sum[:16])
	release()
	return key, true
}
