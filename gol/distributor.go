package gol

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events    chan<- Event
	ioCommand chan<- ioCommand
	ioIdle    <-chan bool

	input    <-chan uint8
	output   chan<- uint8
	filename chan<- string

	keyPresses <-chan rune
}

const alive = 255
const dead = 0

//Calculate the mod of two integers
func mod(x, m int) int {
	return (x + m) % m
}

//Make the image data to be stored in 2-dimentional byte form
func makeMatrix(height, width int) [][]byte {
	matrix := make([][]byte, height)
	for i := range matrix {
		matrix[i] = make([]byte, width)
	}
	return matrix
}

//Make the world data be immuatble
func makeImmutableWorld(matrix [][]byte) func(y, x int) byte {
	return func(y, x int) byte {
		return matrix[y][x]
	}
}

//Counts how many cells are alive around a given cell
func calculateNeighbours(p Params, x, y int, world func(y, x int) byte) int {
	neighbours := 0
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			if i != 0 || j != 0 {
				if world(mod(y+i, p.ImageHeight), mod(x+j, p.ImageWidth)) == alive {
					neighbours++
				}
			}
		}
	}
	return neighbours
}

//Find all cells need to be flipped and flip then give out the starting board of next turn
func calculateNextState(StartY int, EndY int, StartX int, EndX int, p Params, world func(y, x int) byte, turns int, c distributorChannels) [][]byte {
	newWorld := makeMatrix(EndY-StartY, EndX-StartX)

	for y := 0; y < EndY-StartY; y++ {
		Y := y + StartY

		for x := 0; x < EndX-StartX; x++ {
			neighbours := calculateNeighbours(p, x, Y, world)
			if world(Y, x) == alive {
				if neighbours == 2 || neighbours == 3 {
					newWorld[y][x] = alive
				} else {
					newWorld[y][x] = dead
					c.events <- CellFlipped{turns, util.Cell{x, Y}}
				}
			}

			if world(Y, x) == 0 {
				if neighbours == 3 {
					newWorld[y][x] = alive
					c.events <- CellFlipped{turns, util.Cell{x, Y}}
				} else {
					newWorld[y][x] = dead
				}
			}
		}
	}
	return newWorld
}

//Calculate all alive cells on the board in current turn
func calculateAliveCells(p Params, world [][]byte) []util.Cell {
	aliveCells := []util.Cell{}

	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == alive {
				aliveCells = append(aliveCells, util.Cell{X: x, Y: y})
			}
		}
	}
	return aliveCells
}

//Take in one part of the world and do the relating operatinos
func worker(StartY int, EndY int, StartX int, EndX int, p Params, immutableWorld func(y, x int) byte, c distributorChannels, turns int, out chan<- [][]uint8) {
	worldAllocation := calculateNextState(StartY, EndY, StartX, EndX, p, immutableWorld, turns, c)
	out <- worldAllocation
}

//Wrier out the image
func printImage(c distributorChannels, p Params, turn int, world [][]byte) {
	c.ioCommand <- ioOutput
	c.filename <- strings.Join([]string{strconv.Itoa(p.ImageWidth), strconv.Itoa(p.ImageHeight), strconv.Itoa(p.Turns)}, "x")

	for m := 0; m < p.ImageHeight; m++ {
		for n := 0; n < p.ImageWidth; n++ {
			c.output <- world[m][n]
		}
	}

	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       strings.Join([]string{strconv.Itoa(p.ImageWidth), strconv.Itoa(p.ImageHeight), strconv.Itoa(p.Turns)}, "x")}
}

// Divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	//Create a 2D slice to store the world.
	entireWorld := makeMatrix(p.ImageHeight, p.ImageWidth)
	ticker := time.NewTicker(2 * time.Second)

	//Input the image from local
	c.ioCommand <- ioInput
	c.filename <- strings.Join([]string{strconv.Itoa(p.ImageWidth), strconv.Itoa(p.ImageHeight)}, "x")

	//Send a CellFlipped Event to all initally alive cells.
	for i := 0; i < p.ImageHeight; i++ {
		for j := 0; j < p.ImageWidth; j++ {
			a := <-c.input
			entireWorld[i][j] = a
			if a == alive {
				c.events <- CellFlipped{CompletedTurns: 0, Cell: util.Cell{X: i, Y: j}}
			}
		}
	}

	//Execute all turns of the Game of Life.
	turn := 0
	currentTurn := p.Turns

	for currentTurn > 0 {
		immutableWorld := makeImmutableWorld(entireWorld)

		workerHeight := p.ImageHeight / p.Threads
		out := make([]chan [][]byte, p.Threads)
		for i := range out {
			out[i] = make(chan [][]byte)
		}

		if p.Threads < 2 {
			go worker(0, p.ImageHeight, 0, p.ImageWidth, p, immutableWorld, c, turn, out[0])
		} else {
			for i := 0; i < (p.Threads - 1); i++ {
				go worker(i*workerHeight, (i+1)*workerHeight, 0, p.ImageWidth, p, immutableWorld, c, turn, out[i])
			}
			go worker((p.Threads-1)*workerHeight, p.ImageHeight, 0, p.ImageWidth, p, immutableWorld, c, turn, out[p.Threads-1])
		}

		newWorld := makeMatrix(0, 0)
		for j := 0; j < p.Threads; j++ {
			part := <-out[j]
			newWorld = append(newWorld, part...)
		}

		entireWorld = newWorld
		currentTurn--

		c.events <- TurnComplete{turn}
		turn++

		token := false
		select {
		case <-ticker.C:
			c.events <- AliveCellsCount{
				CompletedTurns: turn,
				CellsCount:     len(calculateAliveCells(p, entireWorld))}
		case command := <-c.keyPresses:
			switch command {
			case 's':
				c.events <- StateChange{turn, Executing}
				printImage(c, p, turn, entireWorld)
			case 'p':
				c.events <- StateChange{turn, Paused}
				printImage(c, p, turn, entireWorld)
				check := false
				for {
					key := <-c.keyPresses
					if key == 'p' {
						c.events <- StateChange{turn, Executing}
						fmt.Println("Countinuing")
						c.events <- TurnComplete{turn}
						check = true
					}
					if check {
						break
					}
				}
			case 'q':
				c.events <- StateChange{turn, Quitting}
				printImage(c, p, turn, entireWorld)
				c.events <- TurnComplete{turn}
				token = true
			}
		default:
		}
		if token == true {
			break
		}
	}

	c.ioCommand <- ioOutput
	c.filename <- strings.Join([]string{strconv.Itoa(p.ImageWidth), strconv.Itoa(p.ImageHeight), strconv.Itoa(p.Turns)}, "x")

	for m := 0; m < p.ImageHeight; m++ {
		for n := 0; n < p.ImageWidth; n++ {
			c.output <- entireWorld[m][n]
		}
	}

	// Send correct Events when required
	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       strings.Join([]string{strconv.Itoa(p.ImageWidth), strconv.Itoa(p.ImageHeight), strconv.Itoa(p.Turns)}, "x")}

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- FinalTurnComplete{CompletedTurns: turn, Alive: calculateAliveCells(p, entireWorld)}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
