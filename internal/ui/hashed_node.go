package ui

// HashedNode caches an arbitrary child node and invalidates when its hash changes.
type HashedNode struct {
	BaseNode
	child Node
	hash  uint64
	last  uint64
}

// NewHashedNode constructs a hash-tracked child node.
func NewHashedNode(child Node, hash uint64) *HashedNode {
	return &HashedNode{child: child, hash: hash}
}

// Set replaces the child and hash.
func (n *HashedNode) Set(child Node, hash uint64) {
	if n == nil {
		return
	}
	if n.child == child && n.hash == hash {
		return
	}
	n.child = child
	n.hash = hash
	n.MarkLayoutDirty()
}

// SetHash updates the hash without replacing the child.
func (n *HashedNode) SetHash(hash uint64) {
	if n == nil || n.hash == hash {
		return
	}
	n.hash = hash
	n.MarkDirtyLocal(Rect{W: n.Rect().W, H: n.Rect().H})
}

// Measure measures the current child.
func (n *HashedNode) Measure(ctx *Context, constraints Constraints) Size {
	if n == nil || n.child == nil {
		return constraints.Clamp(Size{})
	}
	return n.child.Measure(ctx, constraints)
}

// Children returns the current child.
func (n *HashedNode) Children() []Node {
	if n == nil || n.child == nil {
		return nil
	}
	return []Node{n.child}
}

// Layout assigns the wrapper bounds to the current child.
func (n *HashedNode) Layout(ctx *Context, rect Rect) {
	n.BaseNode.Layout(ctx, rect)
	if n == nil || n.child == nil {
		return
	}
	n.child.Layout(ctx, rect)
}

// Prepare marks the child dirty when the hash changed.
func (n *HashedNode) Prepare(ctx *Context) {
	if n == nil {
		return
	}
	if n.hash != n.last {
		n.MarkDirtyLocal(Rect{W: n.Rect().W, H: n.Rect().H})
		n.last = n.hash
	}
	if n.child != nil {
		n.child.Prepare(ctx)
	}
}

// Paint paints the current child.
func (n *HashedNode) Paint(ctx *Context, canvas Canvas) {
	if n == nil || n.child == nil || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	n.child.Paint(ctx, canvas)
}
