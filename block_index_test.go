package gfx

import (
	"image"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// bruteVisible returns the blocks Blocks.Draw would draw for the viewport,
// using the same test and in the same order.
func bruteVisible(blocks Blocks, viewport image.Rectangle, origin Vec3) Blocks {
	var visible Blocks

	for _, b := range blocks {
		if b.Rect(origin).Bounds().Overlaps(viewport) {
			visible = append(visible, b)
		}
	}

	return visible
}

func blockIndexFixture() Blocks {
	return Blocks{
		NewBlock(V3(0, 0, 0), V3(10, 10, 10), BlockColorRed),
		NewBlock(V3(40, 0, 0), V3(10, 10, 10), BlockColorGreen),
		NewBlock(V3(0, 40, 0), V3(10, 10, 10), BlockColorBlue),
		NewBlock(V3(40, 40, 0), V3(10, 10, 10), BlockColorYellow),
		NewBlock(V3(20, 20, 20), V3(30, 5, 15), BlockColorPurple),
	}
}

func TestBlockIndexEmpty(t *testing.T) {
	idx := NewBlockIndex()

	if got := idx.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}

	if got := idx.Query(IR(0, 0, 100, 100), V3(50, 50, 1)); got != nil {
		t.Fatalf("Query on empty index = %v, want nil", got)
	}

	if got := idx.QueryBlocks(IR(0, 0, 100, 100), V3(50, 50, 1)); got != nil {
		t.Fatalf("QueryBlocks on empty index = %v, want nil", got)
	}

	if idx.Remove(1) {
		t.Fatal("Remove on empty index returned true")
	}

	if _, ok := idx.Block(1); ok {
		t.Fatal("Block on empty index returned true")
	}

	dst := NewPaletted(8, 8, PaletteGo, PaletteGo[0])
	idx.Draw(dst, V3(0, 0, 1))

	for i, c := range dst.Pix {
		if int(c) != PaletteGo.Index(PaletteGo[0]) {
			t.Fatalf("Draw on empty index modified pixel %d", i)
		}
	}
}

func TestBlockIndexMatchesBruteForce(t *testing.T) {
	blocks := blockIndexFixture()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	if got, want := idx.Len(), len(blocks); got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	for _, origin := range []Vec3{
		V3(50, 50, 1),
		V3(50, 50, 2),
		V3(-30, 20, 0.5),
		V3(400, -100, 3),
		V3(10, 10, -1),
	} {
		for _, viewport := range []image.Rectangle{
			IR(0, 0, 100, 100),
			IR(-200, -200, 200, 200),
			IR(0, 0, 1, 1),
			IR(300, 300, 400, 400),
			IR(-50, -50, 0, 0),
		} {
			want := bruteVisible(blocks, viewport, origin)
			got := idx.QueryBlocks(viewport, origin)

			if !slices.Equal(got, want) {
				t.Fatalf("origin %v viewport %v:\n got %v\nwant %v", origin, viewport, got, want)
			}

			ids := idx.Query(viewport, origin)
			if len(ids) != len(want) {
				t.Fatalf("origin %v viewport %v: Query returned %d IDs, want %d", origin, viewport, len(ids), len(want))
			}

			for i, id := range ids {
				b, ok := idx.Block(id)
				if !ok {
					t.Fatalf("Query returned unknown ID %d", id)
				}
				if b != want[i] {
					t.Fatalf("Query ID %d = %v, want %v", id, b, want[i])
				}
			}
		}
	}
}

func TestBlockIndexUpdate(t *testing.T) {
	idx := NewBlockIndex()

	a := idx.Add(NewBlock(V3(0, 0, 0), V3(10, 10, 10), BlockColorRed))
	b := idx.Add(NewBlock(V3(1000, 0, 0), V3(10, 10, 10), BlockColorGreen))

	viewport := IR(0, 0, 100, 100)
	origin := V3(50, -30, 1)

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a}) {
		t.Fatalf("Query = %v, want [%d]", got, a)
	}

	// Move the far block into view; the change must be visible immediately.
	if !idx.UpdatePos(b, V3(20, 20, 0)) {
		t.Fatal("UpdatePos returned false")
	}

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a, b}) {
		t.Fatalf("after move, Query = %v, want [%d %d]", got, a, b)
	}

	// Move it out again.
	if !idx.UpdatePos(b, V3(5000, 0, 0)) {
		t.Fatal("UpdatePos returned false")
	}

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a}) {
		t.Fatalf("after move out, Query = %v, want [%d]", got, a)
	}

	// Move back, then resize into view.
	if !idx.UpdatePos(b, V3(20, 20, 0)) {
		t.Fatal("UpdatePos returned false")
	}

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a, b}) {
		t.Fatalf("after move back, Query = %v, want [%d %d]", got, a, b)
	}

	if !idx.UpdateSize(b, V3(10000, 10, 10)) {
		t.Fatal("UpdateSize returned false")
	}

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a, b}) {
		t.Fatalf("after resize, Query = %v, want [%d %d]", got, a, b)
	}

	// Shrink back out of view.
	if !idx.UpdateSize(b, V3(10, 10, 10)) {
		t.Fatal("UpdateSize returned false")
	}

	idx.UpdatePos(b, V3(5, 5, 0))

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a, b}) {
		t.Fatalf("after shrink, Query = %v, want [%d %d]", got, a, b)
	}

	// Update must keep insertion order, even after moving across both.
	idx.UpdatePos(a, V3(20, 20, 0))

	if got := idx.Query(viewport, origin); !slices.Equal(got, []BlockID{a, b}) {
		t.Fatalf("order after updates, Query = %v, want [%d %d]", got, a, b)
	}

	// Unknown and removed IDs.
	if idx.Update(12345, NewBlock(V3(0, 0, 0), V3(1, 1, 1), BlockColorRed)) {
		t.Fatal("Update of unknown ID returned true")
	}

	idx.Remove(a)

	if idx.Update(a, NewBlock(V3(0, 0, 0), V3(1, 1, 1), BlockColorRed)) {
		t.Fatal("Update of removed ID returned true")
	}
}

func TestBlockIndexRemoveAndReuse(t *testing.T) {
	idx := NewBlockIndex()

	first := idx.Add(NewBlock(V3(0, 0, 0), V3(10, 10, 10), BlockColorRed))
	second := idx.Add(NewBlock(V3(20, 0, 0), V3(10, 10, 10), BlockColorGreen))

	if !idx.Remove(first) {
		t.Fatal("Remove returned false")
	}

	if idx.Remove(first) {
		t.Fatal("second Remove returned true")
	}

	// The freed slot is reused, but the ID must not come back.
	third := idx.Add(NewBlock(V3(40, 0, 0), V3(10, 10, 10), BlockColorBlue))

	if third == first || third == second {
		t.Fatalf("reused ID %d", third)
	}

	if _, ok := idx.Block(first); ok {
		t.Fatal("removed ID is still present")
	}

	if _, ok := idx.Block(third); !ok {
		t.Fatal("new ID is missing")
	}

	idx.Clear()

	if idx.Len() != 0 {
		t.Fatalf("Len after Clear = %d, want 0", idx.Len())
	}

	if _, ok := idx.Block(third); ok {
		t.Fatal("Clear left an ID present")
	}

	fourth := idx.Add(NewBlock(V3(0, 0, 0), V3(10, 10, 10), BlockColorRed))

	if fourth == first || fourth == second || fourth == third {
		t.Fatalf("Clear reused ID %d", fourth)
	}
}

func TestBlockIndexDuplicateInsertion(t *testing.T) {
	b := NewBlock(V3(10, 10, 10), V3(10, 10, 10), BlockColorRed)

	idx := NewBlockIndex()
	first := idx.Add(b)
	second := idx.Add(b)

	if first == second {
		t.Fatalf("duplicate insertion reused ID %d", first)
	}

	viewport := IR(-100, -100, 100, 100)
	origin := V3(0, 0, 1)

	if got := idx.QueryBlocks(viewport, origin); !slices.Equal(got, Blocks{b, b}) {
		t.Fatalf("QueryBlocks = %v, want two copies", got)
	}

	if !idx.Remove(first) {
		t.Fatal("Remove(first) returned false")
	}

	if got := idx.QueryBlocks(viewport, origin); !slices.Equal(got, Blocks{b}) {
		t.Fatalf("after removing one copy, QueryBlocks = %v, want one copy", got)
	}

	if _, ok := idx.Block(second); !ok {
		t.Fatal("second copy disappeared")
	}
}

func TestBlockIndexDegenerateAndInvalidBlocks(t *testing.T) {
	idx := NewBlockIndex()

	zero := idx.Add(Block{})
	if !zero.Valid() {
		t.Fatal("zero Block was rejected")
	}

	viewport := IR(-100, -100, 100, 100)
	origin := V3(0, 0, 1)

	if got := idx.QueryBlocks(viewport, origin); len(got) != 0 {
		t.Fatalf("degenerate block reported visible: %v", got)
	}

	if want := bruteVisible(Blocks{{}}, viewport, origin); len(want) != 0 {
		t.Fatalf("brute force disagrees about degenerate block: %v", want)
	}

	for _, tc := range []struct {
		name string
		pos  Vec3
		size Vec3
	}{
		{"NaN position", V3(math.NaN(), 0, 0), V3(1, 1, 1)},
		{"NaN size", V3(0, 0, 0), V3(1, math.NaN(), 1)},
		{"Inf position", V3(math.Inf(1), math.Inf(1), 0), V3(1, 1, 1)},
		{"Inf size", V3(0, 0, 0), V3(math.Inf(-1), 1, 1)},
	} {
		id := idx.Add(NewBlock(tc.pos, tc.size, BlockColorRed))
		if id != 0 {
			t.Fatalf("%s: Add returned %d, want 0", tc.name, id)
		}
	}

	if got, want := idx.Len(), 1; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	// A valid block can not be updated into an invalid one.
	good := idx.Add(NewBlock(V3(0, 0, 0), V3(1, 1, 1), BlockColorRed))

	if idx.Update(good, NewBlock(V3(math.NaN(), 0, 0), V3(1, 1, 1), BlockColorRed)) {
		t.Fatal("Update to a NaN position returned true")
	}

	if b, ok := idx.Block(good); !ok || b.Pos != V3(0, 0, 0) {
		t.Fatalf("failed Update modified the block: %v %v", b, ok)
	}
}

func TestBlockIndexExtremeCoordinates(t *testing.T) {
	// Large, but small enough that the isometric projection stays finite.
	blocks := Blocks{
		NewBlock(V3(1e300, -1e300, 0), V3(1, 1, 1), BlockColorRed),
		NewBlock(V3(-1e300, 1e300, 0), V3(1, 1, 1), BlockColorGreen),
		NewBlock(V3(1e150, 1e150, 1e150), V3(1e150, 1e150, 1e150), BlockColorBlue),
		NewBlock(V3(math.MaxFloat64/4, 0, 0), V3(0, 0, 0), BlockColorYellow),
	}

	idx := NewBlockIndex()
	if got := idx.AddBlocks(blocks); len(got) != len(blocks) {
		t.Fatalf("AddBlocks returned %d IDs, want %d", len(got), len(blocks))
	}

	for _, origin := range []Vec3{
		V3(0, 0, 1),
		V3(1e300, 1e300, 1e-300),
		V3(-1e300, -1e300, 1e300),
	} {
		for _, viewport := range []image.Rectangle{
			IR(0, 0, 100, 100),
			IR(-100, -100, 100, 100),
			IR(-math.MaxInt32, -math.MaxInt32, math.MaxInt32, math.MaxInt32),
		} {
			want := bruteVisible(blocks, viewport, origin)
			got := idx.QueryBlocks(viewport, origin)

			if !slices.Equal(got, want) {
				t.Fatalf("origin %v viewport %v:\n got %v\nwant %v", origin, viewport, got, want)
			}
		}
	}

	// Projections that overflow float64 are rejected rather than stored with
	// clamped (and therefore wrong) bounds.
	if id := idx.Add(NewBlock(V3(math.MaxFloat64, math.MaxFloat64, math.MaxFloat64), V3(1, 1, 1), BlockColorRed)); id != 0 {
		t.Fatalf("overflowing block accepted with ID %d", id)
	}
}

func TestBlockIndexTangentViewport(t *testing.T) {
	b := NewBlock(V3(5, 5, 5), V3(10, 10, 10), BlockColorRed)
	origin := V3(100, 100, 2)

	r := b.Rect(origin).Bounds()

	idx := NewBlockIndex()
	idx.Add(b)

	// Tangent on each side: sharing an edge is not an overlap.
	tangent := []image.Rectangle{
		IR(r.Max.X, r.Min.Y, r.Max.X+50, r.Max.Y),
		IR(r.Min.X-50, r.Min.Y, r.Min.X, r.Max.Y),
		IR(r.Min.X, r.Max.Y, r.Max.X, r.Max.Y+50),
		IR(r.Min.X, r.Min.Y-50, r.Max.X, r.Min.Y),
	}

	for _, viewport := range tangent {
		if got := idx.QueryBlocks(viewport, origin); len(got) != 0 {
			t.Fatalf("tangent viewport %v reported block visible", viewport)
		}
		if want := bruteVisible(Blocks{b}, viewport, origin); len(want) != 0 {
			t.Fatalf("brute force disagrees for tangent viewport %v", viewport)
		}
	}

	// One pixel inside the edge makes it visible.
	touching := []image.Rectangle{
		IR(r.Max.X-1, r.Min.Y, r.Max.X+49, r.Max.Y),
		IR(r.Min.X-49, r.Min.Y, r.Min.X+1, r.Max.Y),
		IR(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y+49),
		IR(r.Min.X, r.Min.Y-49, r.Max.X, r.Min.Y+1),
	}

	for _, viewport := range touching {
		if got := idx.QueryBlocks(viewport, origin); len(got) != 1 {
			t.Fatalf("viewport %v inside the edge: got %d blocks, want 1", viewport, len(got))
		}
	}
}

func TestBlockIndexTinyScaleOrigin(t *testing.T) {
	// A very small origin.Z makes the viewport-to-isometric conversion
	// overflow; the index must agree with brute force either way.
	blocks := Blocks{
		NewBlock(V3(1e200, 0, 0), V3(1, 1, 1), BlockColorRed),
		NewBlock(V3(0, 0, 0), V3(1, 1, 1), BlockColorGreen),
	}

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	origin := V3(0, 0, 1e-300)
	viewport := IR(0, 0, 100, 100)

	if got, want := idx.QueryBlocks(viewport, origin), bruteVisible(blocks, viewport, origin); !slices.Equal(got, want) {
		t.Fatalf("tiny origin.Z:\n got %v\nwant %v", got, want)
	}
}

func TestBlockIndexZeroScaleOrigin(t *testing.T) {
	blocks := blockIndexFixture()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	viewport := IR(-1000, -1000, 1000, 1000)

	for _, origin := range []Vec3{
		V3(0, 0, 0),
		V3(100, -50, 0),
	} {
		// With Z == 0 every projected Rect has zero area, so nothing is
		// visible; this must agree with the linear scan.
		if got, want := len(idx.QueryBlocks(viewport, origin)), len(bruteVisible(blocks, viewport, origin)); got != want {
			t.Fatalf("origin %v: QueryBlocks returned %d, brute force %d", origin, got, want)
		}
	}

	// An empty viewport must never return anything.
	if got := idx.Query(IR(10, 10, 10, 20), V3(0, 0, 1)); got != nil {
		t.Fatalf("empty viewport returned %v", got)
	}
}

func TestBlockIndexSortMatchesBlocksSort(t *testing.T) {
	// A scene where painter's order differs from insertion order.
	blocks := Blocks{
		NewBlock(V3(0, 0, 0), V3(10, 10, 10), BlockColorRed),
		NewBlock(V3(30, 0, 0), V3(10, 10, 10), BlockColorGreen),
		NewBlock(V3(15, 15, 5), V3(10, 10, 10), BlockColorBlue),
		NewBlock(V3(0, 30, 0), V3(10, 10, 10), BlockColorYellow),
		NewBlock(V3(30, 30, 0), V3(10, 10, 10), BlockColorPurple),
	}

	origin := V3(100, 100, 1)
	viewport := IR(-1000, -1000, 1000, 1000)

	want := slices.Clone(blocks)
	want.Sort()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)
	idx.Sort()

	if got := idx.QueryBlocks(viewport, origin); !slices.Equal(got, want) {
		t.Fatalf("after Sort:\n got %v\nwant %v", got, want)
	}
}

func TestBlockIndexQueryAllocationBounded(t *testing.T) {
	build := func(n int) (*BlockIndex, image.Rectangle, Vec3) {
		idx := NewBlockIndex()

		for i := 0; i < n; i++ {
			x := float64(i%100) * 100
			y := float64(i/100) * 100

			idx.Add(NewBlock(V3(x, y, 0), V3(10, 10, 10), BlockColorRed))
		}

		return idx, IR(0, 0, 100, 100), V3(50, 50, 1)
	}

	small, viewport, origin := build(100)
	large, _, _ := build(10000)

	// Warm up both indexes so scratch buffers are allocated.
	small.Query(viewport, origin)
	large.Query(viewport, origin)

	smallAllocs := testing.AllocsPerRun(20, func() {
		small.Query(viewport, origin)
	})

	largeAllocs := testing.AllocsPerRun(20, func() {
		large.Query(viewport, origin)
	})

	// The per-query allocation is the returned slice (plus possibly one for
	// sorting); it must not grow with the number of indexed blocks.
	if largeAllocs > smallAllocs+1 {
		t.Fatalf("allocations grew with index size: %v for 100 blocks, %v for 10000", smallAllocs, largeAllocs)
	}

	if largeAllocs > 4 {
		t.Fatalf("Query allocated %v times per run, want at most 4", largeAllocs)
	}

	// Scratch memory is bounded by the number of visible blocks, not the
	// number of indexed blocks.
	if got, max := cap(large.scratch), large.Len(); got > max {
		t.Fatalf("scratch capacity %d exceeds indexed blocks %d", got, max)
	}
}

func TestBlockIndexSparseSceneMatchesBruteForce(t *testing.T) {
	const side = 100 // side*side = 10000 blocks

	// Deterministic but not trivially aligned: mixed sizes, overlapping
	// projections, negative coordinates and negative Z sizes.
	rng := rand.New(rand.NewSource(89))

	blocks := make(Blocks, 0, side*side)

	for i := 0; i < side*side; i++ {
		pos := V3(
			rng.Float64()*4000-2000,
			rng.Float64()*4000-2000,
			rng.Float64()*800-400,
		)
		size := V3(
			4+rng.Float64()*40,
			4+rng.Float64()*40,
			4+rng.Float64()*40,
		)

		if i%11 == 0 {
			size.X = -size.X
		}
		if i%13 == 0 {
			size.Y = -size.Y
		}
		if i%17 == 0 {
			size.Z = -size.Z
		}

		blocks = append(blocks, NewBlock(pos, size, BlockColors[i%len(BlockColors)]))
	}

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	origins := []Vec3{
		V3(0, 0, 1),
		V3(500, 500, 2),
		V3(-750, 300, 0.5),
		V3(200, -200, 1.7),
	}

	viewports := []image.Rectangle{
		IR(0, 0, 120, 120),
		IR(-60, -60, 60, 60),
		IR(900, 900, 1100, 1050),
		IR(-1200, -1100, -1000, -900),
	}

	for _, origin := range origins {
		for _, viewport := range viewports {
			want := bruteVisible(blocks, viewport, origin)
			got := idx.QueryBlocks(viewport, origin)

			if !slices.Equal(got, want) {
				t.Fatalf("origin %v viewport %v: visible set differs (got %d, want %d)",
					origin, viewport, len(got), len(want))
			}

			// The sparse scene must not make queries linear.
			if len(got) >= len(blocks)/10 {
				t.Fatalf("viewport %v: %d of %d blocks visible, scene is not sparse", viewport, len(got), len(blocks))
			}

			// Compare the rendered image too. *Paletted supports arbitrary
			// bounds, so the destination is exactly the viewport and both
			// draw paths see the same bounds.
			brute := NewPalettedImage(viewport, PaletteGo)
			blocks.Draw(brute, origin)

			fast := NewPalettedImage(viewport, PaletteGo)
			idx.Draw(fast, origin)

			if !slices.Equal(fast.Pix, brute.Pix) {
				t.Fatalf("origin %v viewport %v: indexed Draw differs from Blocks.Draw", origin, viewport)
			}

			// Rendering the visible set by hand must also match.
			manual := NewPalettedImage(viewport, PaletteGo)
			for _, b := range got {
				b.Draw(manual, origin)
			}

			if !slices.Equal(manual.Pix, brute.Pix) {
				t.Fatalf("origin %v viewport %v: QueryBlocks + Draw differs from Blocks.Draw", origin, viewport)
			}
		}
	}
}

func TestBlockIndexAppendQuery(t *testing.T) {
	blocks := blockIndexFixture()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	viewport := IR(-200, -200, 200, 200)
	origin := V3(50, 50, 1)

	want := idx.Query(viewport, origin)

	buf := make([]BlockID, 0, len(want))
	got := idx.AppendQuery(buf, viewport, origin)

	if !slices.Equal(got, want) {
		t.Fatalf("AppendQuery = %v, want %v", got, want)
	}

	if cap(got) != cap(buf) {
		t.Fatalf("AppendQuery allocated instead of using the provided buffer")
	}

	if q := append(got, 12345); len(q) != len(want)+1 {
		t.Fatalf("unexpected append behavior")
	}
}

func TestBlockIndexDrawVariantsMatchBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(11))

	var blocks Blocks

	for i := 0; i < 120; i++ {
		pos := V3(
			rng.Float64()*400-200,
			rng.Float64()*400-200,
			rng.Float64()*120-60,
		)
		size := V3(
			8+rng.Float64()*50,
			8+rng.Float64()*50,
			8+rng.Float64()*50,
		)
		blocks = append(blocks, NewBlock(pos, size, BlockColors[i%len(BlockColors)]))
	}

	blocks.Sort()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	origin := V3(200, 200, 1)

	variants := []struct {
		name string
		want func(*Paletted, Vec3)
		got  func(*Paletted, Vec3)
	}{
		{
			"DrawPolygons",
			func(dst *Paletted, o Vec3) { blocks.DrawPolygons(dst, o) },
			func(dst *Paletted, o Vec3) { idx.DrawPolygons(dst, o) },
		},
		{
			"DrawRectangles",
			func(dst *Paletted, o Vec3) { blocks.DrawRectangles(dst, o) },
			func(dst *Paletted, o Vec3) { idx.DrawRectangles(dst, o) },
		},
		{
			"DrawBounds",
			func(dst *Paletted, o Vec3) { blocks.DrawBounds(dst, o) },
			func(dst *Paletted, o Vec3) { idx.DrawBounds(dst, o) },
		},
		{
			"DrawWireframes",
			func(dst *Paletted, o Vec3) { blocks.DrawWireframes(dst, o) },
			func(dst *Paletted, o Vec3) { idx.DrawWireframes(dst, o) },
		},
	}

	for _, v := range variants {
		want := NewPaletted(240, 180, PaletteGo, PaletteGo[0])
		v.want(want, origin)

		got := NewPaletted(240, 180, PaletteGo, PaletteGo[0])
		v.got(got, origin)

		if !slices.Equal(got.Pix, want.Pix) {
			t.Fatalf("%s: indexed draw differs from Blocks method", v.name)
		}
	}
}

func TestBlockIndexDrawMatchesBlocksDraw(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	var blocks Blocks

	for i := 0; i < 200; i++ {
		pos := V3(
			rng.Float64()*600-300,
			rng.Float64()*600-300,
			rng.Float64()*200-100,
		)
		size := V3(
			5+rng.Float64()*60,
			5+rng.Float64()*60,
			5+rng.Float64()*60,
		)
		blocks = append(blocks, NewBlock(pos, size, BlockColors[i%len(BlockColors)]))
	}

	// Painter's order, as Blocks.Draw expects.
	blocks.Sort()

	idx := NewBlockIndex()
	idx.AddBlocks(blocks)

	for _, origin := range []Vec3{
		V3(300, 300, 1),
		V3(-200, 150, 2),
		V3(400, 400, 0.6),
	} {
		want := NewPaletted(240, 180, PaletteGo, PaletteGo[0])
		blocks.Draw(want, origin)

		got := NewPaletted(240, 180, PaletteGo, PaletteGo[0])
		idx.Draw(got, origin)

		if !slices.Equal(got.Pix, want.Pix) {
			t.Fatalf("origin %v: indexed Draw differs from Blocks.Draw", origin)
		}
	}
}
