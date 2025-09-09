package main

import (
	"context"
	"log"
	"time"

	"github.com/libraries/daze/lib/priority"
)

const (
	cGo             = 4
	cPriorityLevels = 3
	cTick           = time.Millisecond
)

func main() {
	pri := priority.NewPriority(cPriorityLevels)
	ret := make([]uint64, cPriorityLevels)
	ctx, fin := context.WithTimeout(context.Background(), time.Second*4)
	for i := range cPriorityLevels * cGo {
		p := i % cPriorityLevels
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					pri.Pri(p, func() error {
						ret[p] += 1
						time.Sleep(cTick)
						return nil
					})
				}
			}
		}()
	}
	<-ctx.Done()
	fin()
	for i := range cPriorityLevels {
		log.Printf("main: report level=%d count=%d", i, ret[i])
	}
}
