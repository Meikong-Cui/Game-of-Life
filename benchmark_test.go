package main

import (
	"fmt"
	"os"
	"testing"

	"uk.ac.bris.cs/gameoflife/gol"
)

func BenchmarkGol(b *testing.B) {
	os.Stdout = nil
	tests := []gol.Params{
		{ImageWidth: 16, ImageHeight: 16},
		//{ImageWidth: 64, ImageHeight: 64},
		//{ImageWidth: 512, ImageHeight: 512},
	}

	for _, p := range tests {
		for _, turns := range []int{10} {
			p.Turns = turns
			for threads := 1; threads <= 3; threads++ {
				p.Threads = threads
				testName := fmt.Sprintf("%dx%dx%d-%d", p.ImageWidth, p.ImageHeight, p.Turns, p.Threads)
				b.Run(testName, func(b *testing.B) {
					for i := 0; i < b.N; i++ {
						events := make(chan gol.Event)
						gol.Run(p, events, nil)
					}
				})
			}
		}
	}
}
