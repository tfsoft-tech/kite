package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := os.Args[1]
	conns, _ := strconv.Atoi(os.Args[2])
	dur, _ := time.ParseDuration(os.Args[3])
	tr := &http.Transport{MaxIdleConnsPerHost: conns, MaxConnsPerHost: conns}
	cl := &http.Client{Transport: tr}
	var n, errs atomic.Int64
	stop := time.Now().Add(dur)
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				resp, err := cl.Get(url)
				if err != nil || resp.StatusCode != 200 {
					errs.Add(1)
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("%.0f %d\n", float64(n.Load())/dur.Seconds(), errs.Load())
}
