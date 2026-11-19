package gfx

import (
	"cmp"
	"image"
	"image/draw"
	"math"
	"slices"
)

// BlockID identifies a Block that has been added to a BlockIndex.
//
// The zero BlockID is invalid. A slot's ID changes every time the slot is
// reused, so an ID handed out before a block was removed stays invalid instead
// of silently referring to whichever block ended up in that slot.
type BlockID uint64

// Valid reports whether id has the shape of an ID handed out by a BlockIndex.
// It does not report whether that block is still present; use
// BlockIndex.Block or BlockIndex.Remove for that.
func (id BlockID) Valid() bool { return id != 0 }

// BlockIndex is a mutable spatial index of Blocks for isometric rendering.
//
// It is intended for scenes with many blocks where only a small part is
// visible at a time. Blocks can be added, removed, moved and resized in place;
// queries always reflect the current state without a rebuild.
//
// The index stores every block with the axis-aligned bounds of its projected
// corners in the origin-independent isometric (h, v) plane used by Block. A
// query converts the screen viewport into that plane for the given origin,
// visits only the tree nodes that can intersect it, and then applies exactly
// the same test as Blocks.Draw (Block.Rect(origin).Bounds().Overlaps(viewport))
// to every candidate. The returned set is therefore identical to a full linear
// scan, including for overlapping projections, non-uniform or negative sizes,
// and negative coordinates.
//
// Results are returned in insertion order, which is the order Blocks.Draw
// paints a Blocks slice. Add blocks in the order they should be painted (for
// example after calling Blocks.Sort) to preserve the painter's algorithm.
// Updating a block keeps its place in that order; removing and re-adding it
// moves it to the end.
//
// A BlockIndex is not safe for concurrent use. It must not be copied after
// first use.
type BlockIndex struct {
	root  *blockIndexNode
	slots []blockIndexSlot
	free  []int32

	// freeNodes recycles tree nodes released by Remove and Update, so a
	// steady-state scene can update blocks without allocating.
	freeNodes []*blockIndexNode

	live int
	// seq is the insertion sequence number given to the next added block.
	seq uint64

	// scratch holds the visible slot indices of the query in progress. Its
	// capacity is reused between calls and is bounded by the largest number
	// of visible blocks seen so far, not by the size of the index.
	scratch []int32
}

// blockIndexSlot is one storage entry of a BlockIndex.
type blockIndexSlot struct {
	block      Block
	bounds     blockIndexBounds
	node       *blockIndexNode
	id         BlockID
	present    bool
	generation uint32
	// seq is a strictly increasing insertion number. It is used to return
	// query results in insertion order after slots have been recycled.
	seq uint64
}

// blockIndexBounds is an axis-aligned box in the isometric (h, v) plane
// produced by spaceToIso, before the origin is applied.
type blockIndexBounds struct {
	minH, minV float64
	maxH, maxV float64
}

func (b blockIndexBounds) union(c blockIndexBounds) blockIndexBounds {
	return blockIndexBounds{
		minH: math.Min(b.minH, c.minH),
		minV: math.Min(b.minV, c.minV),
		maxH: math.Max(b.maxH, c.maxH),
		maxV: math.Max(b.maxV, c.maxV),
	}
}

// overlaps reports whether b and c share any area. It uses the same strict
// comparison as image.Rectangle.Overlaps, so touching edges do not overlap.
func (b blockIndexBounds) overlaps(c blockIndexBounds) bool {
	return b.minH < c.maxH && c.minH < b.maxH &&
		b.minV < c.maxV && c.minV < b.maxV
}

// area is used by the insertion heuristic. It may be +Inf or NaN for
// degenerate bounds; that only affects tree shape, never correctness.
func (b blockIndexBounds) area() float64 {
	return (b.maxH - b.minH) * (b.maxV - b.minV)
}

// blockIndexNode is a node in the AABB tree. Internal nodes have two children;
// leaves refer to a slot. A nil root means the index is empty.
type blockIndexNode struct {
	bounds blockIndexBounds
	parent *blockIndexNode
	left   *blockIndexNode
	right  *blockIndexNode
	height int32
	// slot is the index into BlockIndex.slots for leaves, -1 otherwise.
	slot int32
}

func (n *blockIndexNode) leaf() bool { return n != nil && n.slot >= 0 }

func (n *blockIndexNode) nodeHeight() int32 {
	if n == nil {
		return 0
	}
	return n.height
}

func (n *blockIndexNode) recompute() {
	n.height = 1 + max(n.left.nodeHeight(), n.right.nodeHeight())
	n.bounds = n.left.bounds.union(n.right.bounds)
}

// NewBlockIndex returns an empty BlockIndex.
func NewBlockIndex() *BlockIndex {
	return &BlockIndex{}
}

// Len returns the number of blocks currently in the index.
func (idx *BlockIndex) Len() int {
	if idx == nil {
		return 0
	}
	return idx.live
}

// Add adds b to the index and returns its ID.
//
// Adding the same Block value more than once creates separate entries with
// separate IDs; removing one leaves the others in place. The zero Block is
// accepted, but its projection has zero area, so it is never reported as
// visible (exactly like Blocks.Draw, which skips it).
//
// A block with a NaN or infinite coordinate in Pos or Size is not added and
// Add returns 0. Such a block has no meaningful projection: its pixel bounds
// depend on the platform's float-to-int conversion, so it cannot be indexed in
// a way that is guaranteed to agree with Blocks.Draw. Very large but finite
// coordinates are indexed normally.
func (idx *BlockIndex) Add(b Block) BlockID {
	bounds, ok := blockIndexBoundsFor(b)
	if !ok {
		return 0
	}

	slotIndex := idx.allocateSlot()
	slot := &idx.slots[slotIndex]

	idx.seq++
	slot.block = b
	slot.bounds = bounds
	slot.present = true
	slot.seq = idx.seq
	// The generation starts at 1 and is incremented every time the slot is
	// reused, so an ID from a previous occupant is rejected.
	slot.id = blockIndexID(slotIndex, slot.generation)

	idx.insertLeaf(slotIndex)
	idx.live++

	return slot.id
}

// AddNewBlock creates a Block and adds it to the index.
func (idx *BlockIndex) AddNewBlock(pos, size Vec3, ic BlockColor) BlockID {
	return idx.Add(NewBlock(pos, size, ic))
}

// AddAll adds all the given blocks and returns their IDs in the same order.
// Blocks that cannot be indexed have ID 0 in the returned slice.
func (idx *BlockIndex) AddAll(blocks ...Block) []BlockID {
	ids := make([]BlockID, len(blocks))
	for i, b := range blocks {
		ids[i] = idx.Add(b)
	}
	return ids
}

// AddBlocks adds all blocks in the given slice.
func (idx *BlockIndex) AddBlocks(blocks Blocks) []BlockID {
	return idx.AddAll(blocks...)
}

// Remove removes the block with the given ID. It returns false if the ID is
// unknown, stale, or already removed.
func (idx *BlockIndex) Remove(id BlockID) bool {
	slotIndex, ok := idx.slotIndex(id)
	if !ok {
		return false
	}

	idx.removeLeaf(idx.slots[slotIndex].node)

	// Keep the generation so the next occupant gets a fresh ID.
	gen := idx.slots[slotIndex].generation
	idx.slots[slotIndex] = blockIndexSlot{generation: gen}
	idx.free = append(idx.free, slotIndex)
	idx.live--

	return true
}

// Block returns the block stored under id and whether it is present.
func (idx *BlockIndex) Block(id BlockID) (Block, bool) {
	slotIndex, ok := idx.slotIndex(id)
	if !ok {
		return Block{}, false
	}
	return idx.slots[slotIndex].block, true
}

// Update replaces the block stored under id with b. The block keeps its ID and
// its position in the insertion order, so drawing order is unaffected.
//
// It returns false, leaving the index untouched, if id is unknown or if b
// cannot be indexed (see Add).
func (idx *BlockIndex) Update(id BlockID, b Block) bool {
	slotIndex, ok := idx.slotIndex(id)
	if !ok {
		return false
	}

	bounds, ok := blockIndexBoundsFor(b)
	if !ok {
		return false
	}

	slot := &idx.slots[slotIndex]
	slot.block = b

	if slot.bounds != bounds {
		idx.removeLeaf(slot.node)
		slot.bounds = bounds
		idx.insertLeaf(slotIndex)
	}

	return true
}

// UpdatePos moves the block under id to pos.
func (idx *BlockIndex) UpdatePos(id BlockID, pos Vec3) bool {
	b, ok := idx.Block(id)
	if !ok {
		return false
	}
	b.Pos = pos
	return idx.Update(id, b)
}

// UpdateSize resizes the block under id, keeping its position.
func (idx *BlockIndex) UpdateSize(id BlockID, size Vec3) bool {
	b, ok := idx.Block(id)
	if !ok {
		return false
	}
	b.Size = size
	return idx.Update(id, b)
}

// Sort reorders the blocks in the index using Block.Behind, the same
// comparison as Blocks.Sort. After a call, queries and Draw return blocks in
// painter's order (the order Blocks.Draw would paint a sorted Blocks slice).
//
// Sort is not maintained automatically: moving blocks can change their
// relative front/back relationship, so call Sort again after such updates if
// painter's order matters for your scene.
func (idx *BlockIndex) Sort() {
	if idx.live < 2 {
		return
	}

	// Collect the present slots in insertion order and sort their blocks
	// exactly like Blocks.Sort does.
	order := make([]int32, 0, idx.live)
	for i := range idx.slots {
		if idx.slots[i].present {
			order = append(order, int32(i))
		}
	}

	slices.SortFunc(order, func(a, b int32) int {
		return cmp.Compare(idx.slots[a].seq, idx.slots[b].seq)
	})

	blocks := make(Blocks, len(order))
	for i, slotIndex := range order {
		blocks[i] = idx.slots[slotIndex].block
	}
	blocks.Sort()

	// Relabel sequence numbers in the new order. Identical blocks are
	// interchangeable, so a value keyed FIFO of slots is enough to keep every
	// slot accounted for.
	slotsByBlock := make(map[Block][]int32, len(order))
	for _, slotIndex := range order {
		b := idx.slots[slotIndex].block
		slotsByBlock[b] = append(slotsByBlock[b], slotIndex)
	}

	for _, b := range blocks {
		queue := slotsByBlock[b]
		slotIndex := queue[0]
		slotsByBlock[b] = queue[1:]

		idx.seq++
		idx.slots[slotIndex].seq = idx.seq
	}
}

// Clear removes every block from the index, keeping allocated storage for
// reuse. Previously returned IDs stay invalid.
func (idx *BlockIndex) Clear() {
	idx.releaseTree(idx.root)
	idx.root = nil

	for i := range idx.slots {
		if idx.slots[i].present {
			gen := idx.slots[i].generation
			idx.slots[i] = blockIndexSlot{generation: gen}
			idx.free = append(idx.free, int32(i))
		}
	}

	idx.live = 0
}

// releaseTree returns every node in the tree to the recycle list.
func (idx *BlockIndex) releaseTree(n *blockIndexNode) {
	if n == nil {
		return
	}
	idx.releaseTree(n.left)
	idx.releaseTree(n.right)
	idx.releaseNode(n)
}

// Query returns the IDs of all blocks whose projected Rect overlaps viewport
// for the given origin, in insertion order.
//
// The result is exactly the set of blocks that Blocks.Draw would draw on a
// destination with these bounds. The returned slice is owned by the caller.
// Empty results return nil. Query allocates proportional to the number of
// hits; its internal working memory is reused and bounded by the largest hit
// count observed.
func (idx *BlockIndex) Query(viewport image.Rectangle, origin Vec3) []BlockID {
	slots := idx.visible(viewport, origin)
	if len(slots) == 0 {
		return nil
	}

	ids := make([]BlockID, len(slots))
	for i, slotIndex := range slots {
		ids[i] = idx.slots[slotIndex].id
	}
	return ids
}

// AppendQuery appends the IDs of the visible blocks to dst and returns the
// extended slice. It is like Query, but lets the caller reuse a buffer, so a
// steady-state scene can query without allocating.
func (idx *BlockIndex) AppendQuery(dst []BlockID, viewport image.Rectangle, origin Vec3) []BlockID {
	for _, slotIndex := range idx.visible(viewport, origin) {
		dst = append(dst, idx.slots[slotIndex].id)
	}
	return dst
}

// QueryBlocks returns the visible blocks themselves, in insertion order. The
// returned slice is owned by the caller.
func (idx *BlockIndex) QueryBlocks(viewport image.Rectangle, origin Vec3) Blocks {
	slots := idx.visible(viewport, origin)
	if len(slots) == 0 {
		return nil
	}

	blocks := make(Blocks, len(slots))
	for i, slotIndex := range slots {
		blocks[i] = idx.slots[slotIndex].block
	}
	return blocks
}

// Draw draws every block that can be visible in dst, in insertion order.
//
// It visits only the part of the index that intersects dst.Bounds() and uses
// the same visibility test and the same origin as Blocks.Draw, so the
// resulting image is identical to drawing the index's blocks with Blocks.Draw
// (assuming they were added in the order Blocks.Draw would paint them).
//
// Draw uses Query internally, so it allocates proportional to the number of
// visible blocks. Use AppendQuery with your own buffer, and draw the returned
// blocks yourself, if a frame budget cannot afford that.
func (idx *BlockIndex) Draw(dst draw.Image, origin Vec3) {
	for _, slotIndex := range idx.visible(dst.Bounds(), origin) {
		idx.slots[slotIndex].block.Draw(dst, origin)
	}
}

// DrawPolygons draws every block that can be visible in dst, in insertion
// order, using Block.DrawPolygons. See Draw.
func (idx *BlockIndex) DrawPolygons(dst draw.Image, origin Vec3) {
	for _, slotIndex := range idx.visible(dst.Bounds(), origin) {
		idx.slots[slotIndex].block.DrawPolygons(dst, origin)
	}
}

// DrawRectangles draws every block that can be visible in dst, in insertion
// order, using Block.DrawRectangles. See Draw.
func (idx *BlockIndex) DrawRectangles(dst draw.Image, origin Vec3) {
	for _, slotIndex := range idx.visible(dst.Bounds(), origin) {
		idx.slots[slotIndex].block.DrawRectangles(dst, origin)
	}
}

// DrawBounds draws every block that can be visible in dst, in insertion
// order, using Block.DrawBounds. See Draw.
func (idx *BlockIndex) DrawBounds(dst draw.Image, origin Vec3) {
	for _, slotIndex := range idx.visible(dst.Bounds(), origin) {
		idx.slots[slotIndex].block.DrawBounds(dst, origin)
	}
}

// DrawWireframes draws every block that can be visible in dst, in insertion
// order, using Block.DrawWireframe. See Draw.
func (idx *BlockIndex) DrawWireframes(dst draw.Image, origin Vec3) {
	for _, slotIndex := range idx.visible(dst.Bounds(), origin) {
		idx.slots[slotIndex].block.DrawWireframe(dst, origin)
	}
}

// visible fills and sorts idx.scratch with the visible slot indices.
func (idx *BlockIndex) visible(viewport image.Rectangle, origin Vec3) []int32 {
	if idx == nil || idx.root == nil || viewport.Empty() {
		return nil
	}

	idx.scratch = idx.scratch[:0]

	if math.IsNaN(origin.X) || math.IsNaN(origin.Y) || math.IsNaN(origin.Z) ||
		math.IsInf(origin.X, 0) || math.IsInf(origin.Y, 0) || math.IsInf(origin.Z, 0) {
		// A non-finite origin cannot be converted to a finite query region.
		// Fall back to the exact test over every block. This is the only
		// path that is linear in the number of blocks.
		idx.collectLinear(viewport, origin)
	} else if origin.Z != 0 {
		want, ok := blockIndexViewportBounds(viewport, origin)
		if !ok {
			// The isometric query region overflowed float64 (possible for a
			// very small but non-zero origin.Z). Fall back to the exact test
			// rather than risk excluding a visible block.
			idx.collectLinear(viewport, origin)
		} else {
			idx.collect(idx.root, want, viewport, origin)
		}
	}
	// origin.Z == 0 collapses every projected Rect to zero width and height
	// (Rect.Min == Rect.Max on both axes), so Bounds().Overlaps is always
	// false. Nothing can be visible.

	if len(idx.scratch) == 0 {
		return nil
	}

	slices.SortFunc(idx.scratch, func(a, b int32) int {
		return cmp.Compare(idx.slots[a].seq, idx.slots[b].seq)
	})

	return idx.scratch
}

// collect visits the nodes under n that intersect want and appends the leaves
// that pass the exact visibility test.
func (idx *BlockIndex) collect(n *blockIndexNode, want blockIndexBounds, viewport image.Rectangle, origin Vec3) {
	if n == nil || !n.bounds.overlaps(want) {
		return
	}

	if n.leaf() {
		if blockIndexVisible(idx.slots[n.slot].block, viewport, origin) {
			idx.scratch = append(idx.scratch, n.slot)
		}
		return
	}

	idx.collect(n.left, want, viewport, origin)
	idx.collect(n.right, want, viewport, origin)
}

func (idx *BlockIndex) collectLinear(viewport image.Rectangle, origin Vec3) {
	for i := range idx.slots {
		if idx.slots[i].present && blockIndexVisible(idx.slots[i].block, viewport, origin) {
			idx.scratch = append(idx.scratch, int32(i))
		}
	}
}

// blockIndexVisible is exactly the test Blocks.Draw performs.
func blockIndexVisible(b Block, viewport image.Rectangle, origin Vec3) bool {
	return b.Rect(origin).Bounds().Overlaps(viewport)
}

// blockIndexViewportBounds returns a superset, in the isometric plane, of the
// region whose projection can overlap viewport. The exact test applied to each
// candidate removes the extra area. It reports false if the result is not
// finite (the arithmetic overflowed), in which case no meaningful node filter
// can be derived.
func blockIndexViewportBounds(viewport image.Rectangle, origin Vec3) (blockIndexBounds, bool) {
	// Bounds() truncates each coordinate towards zero, so a block can touch
	// the viewport even when its float coordinates lie just outside it. A
	// one pixel margin covers the truncation; two are used for headroom.
	const pad = 2.0

	x0 := float64(viewport.Min.X) - pad
	x1 := float64(viewport.Max.X) + pad
	y0 := float64(viewport.Min.Y) - pad
	y1 := float64(viewport.Max.Y) + pad

	// x = h*origin.Z + origin.X and y = -(v*origin.Z + origin.Y), so
	// h = (x-origin.X)/origin.Z and v = -(y+origin.Y)/origin.Z.
	h0 := (x0 - origin.X) / origin.Z
	h1 := (x1 - origin.X) / origin.Z
	v0 := -(y0 + origin.Y) / origin.Z
	v1 := -(y1 + origin.Y) / origin.Z

	bounds := blockIndexBounds{
		minH: math.Min(h0, h1),
		minV: math.Min(v0, v1),
		maxH: math.Max(h0, h1),
		maxV: math.Max(v0, v1),
	}

	if !blockIndexFinite2(bounds.minH, bounds.minV) || !blockIndexFinite2(bounds.maxH, bounds.maxV) {
		return blockIndexBounds{}, false
	}

	return bounds, true
}

// blockIndexBoundsFor computes the isometric bounds of a block. It reports
// false when the block has a non-finite coordinate or its projection is not
// finite.
func blockIndexBoundsFor(b Block) (blockIndexBounds, bool) {
	if !blockIndexFinite(b.Pos) || !blockIndexFinite(b.Size) {
		return blockIndexBounds{}, false
	}

	p, s := b.Pos, b.Size

	// The eight corners of the block. Only the axis-aligned extent in the
	// isometric plane matters, but enumerating the corners keeps this correct
	// for negative sizes too.
	corners := [...]Vec3{
		V3(p.X, p.Y, p.Z),
		V3(p.X+s.X, p.Y, p.Z),
		V3(p.X, p.Y+s.Y, p.Z),
		V3(p.X, p.Y, p.Z+s.Z),
		V3(p.X+s.X, p.Y+s.Y, p.Z),
		V3(p.X+s.X, p.Y, p.Z+s.Z),
		V3(p.X, p.Y+s.Y, p.Z+s.Z),
		V3(p.X+s.X, p.Y+s.Y, p.Z+s.Z),
	}

	h, v := spaceToIso(corners[0])

	if !blockIndexFinite2(h, v) {
		return blockIndexBounds{}, false
	}

	bounds := blockIndexBounds{minH: h, maxH: h, minV: v, maxV: v}

	for _, corner := range corners[1:] {
		h, v := spaceToIso(corner)

		if !blockIndexFinite2(h, v) {
			return blockIndexBounds{}, false
		}

		bounds.minH = math.Min(bounds.minH, h)
		bounds.minV = math.Min(bounds.minV, v)
		bounds.maxH = math.Max(bounds.maxH, h)
		bounds.maxV = math.Max(bounds.maxV, v)
	}

	return bounds, true
}

func blockIndexFinite(v Vec3) bool {
	return blockIndexFinite2(v.X, v.Y) && !math.IsInf(v.Z, 0) && !math.IsNaN(v.Z)
}

func blockIndexFinite2(a, b float64) bool {
	return !math.IsInf(a, 0) && !math.IsNaN(a) && !math.IsInf(b, 0) && !math.IsNaN(b)
}

// slotIndex resolves an ID to a slot index, rejecting stale IDs.
func (idx *BlockIndex) slotIndex(id BlockID) (int32, bool) {
	if idx == nil || !id.Valid() {
		return 0, false
	}

	slotIndex := int32(id>>32) - 1
	if slotIndex < 0 || int(slotIndex) >= len(idx.slots) {
		return 0, false
	}

	slot := &idx.slots[slotIndex]
	if !slot.present || slot.id != id {
		return 0, false
	}

	return slotIndex, true
}

func blockIndexID(slotIndex int32, generation uint32) BlockID {
	return BlockID(uint64(uint32(slotIndex)+1)<<32 | uint64(generation))
}

func (idx *BlockIndex) allocateSlot() int32 {
	if n := len(idx.free); n > 0 {
		slotIndex := idx.free[n-1]
		idx.free = idx.free[:n-1]
		idx.slots[slotIndex].generation++
		if idx.slots[slotIndex].generation == 0 {
			idx.slots[slotIndex].generation = 1
		}
		return slotIndex
	}

	slotIndex := int32(len(idx.slots))
	idx.slots = append(idx.slots, blockIndexSlot{generation: 1})
	return slotIndex
}

// insertLeaf adds a leaf for the slot to the tree and rebalances it.
func (idx *BlockIndex) insertLeaf(slotIndex int32) {
	slot := &idx.slots[slotIndex]
	leaf := idx.newLeafNode(slotIndex, slot.bounds)
	slot.node = leaf

	if idx.root == nil {
		idx.root = leaf
		return
	}

	// Find the sibling whose bounds grow the least when the new leaf is
	// added, breaking ties towards the shorter subtree. This is the usual
	// AABB tree insertion heuristic.
	sibling := idx.root
	for !sibling.leaf() {
		left, right := sibling.left, sibling.right
		leftCost := left.bounds.union(leaf.bounds).area() - left.bounds.area()
		rightCost := right.bounds.union(leaf.bounds).area() - right.bounds.area()

		switch {
		case leftCost == rightCost:
			if left.nodeHeight() <= right.nodeHeight() {
				sibling = left
			} else {
				sibling = right
			}
		case leftCost < rightCost:
			sibling = left
		default:
			sibling = right
		}
	}

	oldParent := sibling.parent
	newParent := idx.newInternalNode()
	newParent.parent = oldParent
	newParent.left = sibling
	newParent.right = leaf
	newParent.recompute()

	switch {
	case oldParent == nil:
		idx.root = newParent
	case oldParent.left == sibling:
		oldParent.left = newParent
	default:
		oldParent.right = newParent
	}

	sibling.parent = newParent
	leaf.parent = newParent

	idx.refit(newParent)
}

// removeLeaf detaches a leaf from the tree and rebalances it.
func (idx *BlockIndex) removeLeaf(leaf *blockIndexNode) {
	if leaf == nil {
		return
	}

	if leaf == idx.root {
		idx.root = nil
		idx.releaseNode(leaf)
		return
	}

	parent := leaf.parent
	sibling := parent.left
	if sibling == leaf {
		sibling = parent.right
	}

	grand := parent.parent
	sibling.parent = grand

	if grand == nil {
		idx.root = sibling
	} else {
		if grand.left == parent {
			grand.left = sibling
		} else {
			grand.right = sibling
		}
		idx.refit(grand)
	}

	idx.releaseNode(leaf)
	idx.releaseNode(parent)
}

// newLeafNode returns a reset leaf node for the given slot.
func (idx *BlockIndex) newLeafNode(slotIndex int32, bounds blockIndexBounds) *blockIndexNode {
	n := idx.acquireNode()
	*n = blockIndexNode{bounds: bounds, height: 1, slot: slotIndex}
	return n
}

// newInternalNode returns a reset internal node.
func (idx *BlockIndex) newInternalNode() *blockIndexNode {
	n := idx.acquireNode()
	*n = blockIndexNode{slot: -1}
	return n
}

// acquireNode returns a recycled node or allocates a new one.
func (idx *BlockIndex) acquireNode() *blockIndexNode {
	if n := len(idx.freeNodes); n > 0 {
		node := idx.freeNodes[n-1]
		idx.freeNodes = idx.freeNodes[:n-1]
		return node
	}
	return &blockIndexNode{}
}

// releaseNode returns a detached node to the recycle list.
func (idx *BlockIndex) releaseNode(n *blockIndexNode) {
	if n == nil {
		return
	}
	// Drop pointers so the recycled node does not keep the tree alive.
	*n = blockIndexNode{}
	idx.freeNodes = append(idx.freeNodes, n)
}

// refit recomputes cached bounds and heights from n up to the root, restoring
// the AVL balance property on the way.
func (idx *BlockIndex) refit(n *blockIndexNode) {
	for n != nil {
		n = idx.balance(n)
		n.recompute()
		n = n.parent
	}
}

// balance restores the height balance of the subtree rooted at n and returns
// the new subtree root.
func (idx *BlockIndex) balance(n *blockIndexNode) *blockIndexNode {
	if n == nil || n.leaf() {
		return n
	}

	bf := n.right.nodeHeight() - n.left.nodeHeight()

	switch {
	case bf > 1:
		if n.right.left.nodeHeight() > n.right.right.nodeHeight() {
			n.right = idx.rotateRight(n.right)
		}
		return idx.rotateLeft(n)
	case bf < -1:
		if n.left.right.nodeHeight() > n.left.left.nodeHeight() {
			n.left = idx.rotateLeft(n.left)
		}
		return idx.rotateRight(n)
	}

	return n
}

// rotateLeft rotates the subtree rooted at n left and returns its new root.
func (idx *BlockIndex) rotateLeft(n *blockIndexNode) *blockIndexNode {
	r := n.right
	r.parent = n.parent

	switch {
	case n.parent == nil:
		idx.root = r
	case n.parent.left == n:
		n.parent.left = r
	default:
		n.parent.right = r
	}

	n.right = r.left
	if n.right != nil {
		n.right.parent = n
	}

	r.left = n
	n.parent = r

	n.recompute()
	r.recompute()

	return r
}

// rotateRight rotates the subtree rooted at n right and returns its new root.
func (idx *BlockIndex) rotateRight(n *blockIndexNode) *blockIndexNode {
	l := n.left
	l.parent = n.parent

	switch {
	case n.parent == nil:
		idx.root = l
	case n.parent.left == n:
		n.parent.left = l
	default:
		n.parent.right = l
	}

	n.left = l.right
	if n.left != nil {
		n.left.parent = n
	}

	l.right = n
	n.parent = l

	n.recompute()
	l.recompute()

	return l
}
