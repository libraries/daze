package main

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/libraries/daze/lib/rate"
)

const (
	cGo     = 4
	cLimNum = 128
	cLimPer = time.Millisecond
	cTx     = time.Second * 4
)

func main() {
	lim := rate.NewLimits(cLimNum, cLimPer)
	ctx, fin := context.WithTimeout(context.Background(), cTx)
	for range cGo {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					maxn := uint64(cLimNum) * 2
					step := rand.Uint64() % maxn
					lim.Wait(step)
				}
			}
		}()
	}
	<-ctx.Done()
	fin()
}
