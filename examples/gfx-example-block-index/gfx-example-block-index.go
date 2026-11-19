package main

import (
	"log"

	"github.com/peterhellberg/gfx"
)

func main() {
	const (
		width, height = 640, 400
		spacing       = 16
		side          = 200
	)

	var (
		dst    = gfx.NewPaletted(width, height, gfx.PaletteGo, gfx.PaletteGo[14])
		origin = gfx.V3(320, -1400, 1)
		index  = gfx.NewBlockIndex()
	)

	// A sparse 200x200 grid: 40000 blocks, of which only a few hundred are
	// inside the destination at any given time.
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			pos := gfx.V3(float64(x*spacing), float64(y*spacing), 0)
			size := gfx.V3(spacing, spacing, 8+float64((x*7+y*3)%4)*10)
			color := gfx.BlockColors[(x*x+y*y)%len(gfx.BlockColors)]

			index.AddNewBlock(pos, size, color)
		}
	}

	// Painter's order. The index preserves insertion order in queries and
	// when drawing, so this is the only time the scene needs to be sorted.
	index.Sort()

	// Ask the index which blocks the destination can see. This is exactly
	// the set of blocks Blocks.Draw would visit, without scanning all 6400.
	visible := index.QueryBlocks(dst.Bounds(), origin)
	log.Printf("%d of %d blocks visible", len(visible), index.Len())

	// Draw them in the same order Blocks.Draw would.
	for _, b := range visible {
		b.Draw(dst, origin)
	}

	gfx.SavePNG("gfx-example-block-index.png", dst)
}
