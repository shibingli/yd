package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestReloadSerializesAgainstReads is the regression for the fatal "concurrent map read and map
// write" that a live watch produced: reload mutates viper's maps while request goroutines call
// Get*. Everything now goes through Config.mu. | TestReloadSerializesAgainstReads 是「运行中的监视
// 导致并发读写 map」致命错误的回归：reload 修改 viper 映射时会与请求协程的 Get* 并发。现在一切都
// 经过 Config.mu。
func TestReloadSerializesAgainstReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write := func(value string) {
		if err := os.WriteFile(path, []byte("race:\n  value: "+value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1")

	c, err := Load(Options{ConfigDir: dir, Name: "race", EnvFile: filepath.Join(dir, "missing.env")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.GetInt("race.value")
					_ = c.GetString("race.value")
					_ = c.AllKeys()
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		write("2")
		c.mu.Lock()
		err := c.reload()
		c.mu.Unlock()
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("reload: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
