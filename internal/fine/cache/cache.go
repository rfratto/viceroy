package cache

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
	"go.uber.org/atomic"
)

// Cache implements a cache of nodes and handles that a Handler can use.
type Cache struct {
	log log.Logger

	mut        sync.RWMutex
	nodes      map[fine.Node]*cachedNode
	nodeKeys   map[Key]*cachedNode
	nextID     uint64
	generation uint64

	handleMut    sync.RWMutex
	handles      map[fine.Handle]*cachedHandle
	availHandles []fine.Handle
	nextHandle   fine.Handle
}

type cachedNode struct {
	Node Node
	Info NodeInfo

	refs atomic.Uint64
}

type cachedHandle struct {
	Handle Handle
	Info   HandleInfo
}

type Node interface {
	// Close is called when the Node is fully removed from the cache.
	Close() error
}

type Handle interface {
	// Close is called when the Handle is fully removed from the cache.
	Close() error
}

type NodeInfo struct {
	ID         fine.Node // ID of the Node
	Generation uint64    // Generation of the ID
	Key        Key       // Key used to identify the node
}

type Key struct {
	Parent fine.Node
	Name   string
}

type HandleInfo struct {
	ID fine.Handle
}

// New creates a new cache, pre-populated with a root node.
func New(l log.Logger, rootNode Node) *Cache {
	c := &Cache{
		log: l,

		nodes:    make(map[fine.Node]*cachedNode),
		nodeKeys: make(map[Key]*cachedNode),
		handles:  make(map[fine.Handle]*cachedHandle),
	}

	_, err := c.AddNode(0, "/", rootNode)
	if err != nil {
		panic(err)
	}
	return c
}

// AddNode stores a new node. If the named node already exists, the node
// argument will be ignored and the reference count to the existing node will
// increase.
func (c *Cache) AddNode(parent fine.Node, name string, node Node) (info NodeInfo, err error) {
	c.mut.Lock()
	defer c.mut.Unlock()

	key := Key{Parent: parent, Name: name}
	if n, found := c.nodeKeys[key]; found {
		n.refs.Inc()
		return n.Info, nil
	}

	if parent != 0 {
		_, exist := c.nodes[parent]
		if !exist {
			return info, fmt.Errorf("could not find parent node %d: %w", parent, fine.ErrorStale)
		}
	}

	c.nextID++
	id := c.nextID
	if id == 0 {
		// Our IDs wrapped around. Increase the generation.
		id = 1
		c.generation++

		if c.generation == 0 {
			// Out generations wrapped around. This means we've exhausted the entire
			// (2^64-1)*(2^64-1) space. Seems unlikely, but let's return an error anyway.
			// We'll also reset both the id and generation so the error will repeat
			// on the next request.
			c.generation--
			c.nextID--
			return info, fmt.Errorf("exhausted node ID space: %w", err)
		}

		c.nextID = 1 // nextID is currently 0 (an invalid ID), move it forward
	}

	n := &cachedNode{
		Node: node,
		Info: NodeInfo{
			ID:         fine.Node(id),
			Generation: c.generation,
			Key:        key,
		},
		refs: *atomic.NewUint64(1),
	}

	c.nodes[n.Info.ID] = n
	c.nodeKeys[n.Info.Key] = n
	return n.Info, nil
}

// RenameNode renames a cached file. Returns ErrorNotExist if the source file
// isn't currently cached.
func (c *Cache) RenameNode(parent fine.Node, req *fine.RenameRequest) error {
	c.mut.Lock()
	defer c.mut.Unlock()

	var (
		sourceKey = Key{Parent: parent, Name: req.OldName}
		targetKey = Key{Parent: req.NewDir, Name: req.NewName}
	)

	if _, newParentExist := c.nodes[req.NewDir]; !newParentExist {
		return fmt.Errorf("target directory %d does not exist: %w", req.NewDir, fine.ErrorStale)
	}

	sourceNode, found := c.nodeKeys[sourceKey]
	if !found {
		return fmt.Errorf("source file %s does not exist: %w", req.OldName, fine.ErrorNotExist)
	}

	// Update the node info.
	sourceNode.Info.Key = targetKey

	// Swap keys in the cache.
	if found := c.nodeKeys[sourceKey]; found == sourceNode {
		delete(c.nodeKeys, sourceKey)
	}
	c.nodeKeys[targetKey] = sourceNode
	return nil
}

// ReleaseNode releases a node. refs are subtracted from the total reference
// count, and the node will be fully removed once refs decreases to 0.
func (c *Cache) ReleaseNode(id fine.Node, refs uint64) error {
	var n *cachedNode

	// We close the node in a defer so the lock isn't held for longer than it
	// needs to be.
	defer func() {
		if n == nil || n.Node == nil {
			return
		}
		err := n.Node.Close()
		if err != nil {
			level.Error(c.log).Log("msg", "error when closing stale cache node", "id", id, "err", err)
		}
	}()

	c.mut.Lock()
	defer c.mut.Unlock()

	n, ok := c.nodes[id]
	if !ok {
		return fine.ErrorStale
	}
	if n.refs.Sub(refs) > 0 {
		return nil
	}

	delete(c.nodes, id)

	// Only delete the key if it hasn't been overridden by something like a
	// rename.
	if found := c.nodeKeys[n.Info.Key]; found == n {
		delete(c.nodeKeys, n.Info.Key)
	}
	return nil
}

// GetNode returns the node for ID.
func (c *Cache) GetNode(id fine.Node) (NodeInfo, Node, error) {
	c.mut.RLock()
	defer c.mut.RUnlock()

	n, ok := c.nodes[id]
	if !ok {
		return NodeInfo{}, nil, fine.ErrorStale
	}
	return n.Info, n.Node, nil
}

// GetHandle returns the Handle for a Handle ID.
func (c *Cache) GetHandle(id fine.Handle) (HandleInfo, Handle, error) {
	c.handleMut.RLock()
	defer c.handleMut.RUnlock()

	h, ok := c.handles[id]
	if !ok {
		return HandleInfo{}, nil, fine.ErrorStale
	}
	return h.Info, h.Handle, nil
}

// NodePath returns the full disk path for a node.
func (c *Cache) NodePath(id fine.Node) (string, error) {
	c.mut.RLock()
	defer c.mut.RUnlock()

	start, ok := c.nodes[id]
	if !ok {
		return "", fine.ErrorStale
	}

	// Build up the full file path in reverse.
	var paths []string
	err := c.traverseNodes(start, func(n *cachedNode) {
		paths = append([]string{n.Info.Key.Name}, paths...)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(paths...), nil
}

// traverseNodes traverses the node hierarchy from start until root, calling
// onNode for each node. Returns an error if a parent reference isn't found.
//
// mut must be held while calling traverseNodes.
func (c *Cache) traverseNodes(start *cachedNode, onNode func(*cachedNode)) error {
	cur := start
	for cur != nil {
		onNode(cur)

		parent := cur.Info.Key.Parent
		next, ok := c.nodes[parent]
		if !ok && parent != 0 {
			return fmt.Errorf("could not find parent %d: %w", cur.Info.Key.Parent, fine.ErrorStale)
		}
		cur = next
	}
	return nil
}

// AddHandle stores a new handle.
func (c *Cache) AddHandle(handle Handle) (HandleInfo, error) {
	c.handleMut.Lock()
	defer c.handleMut.Unlock()

	h := &cachedHandle{Handle: handle}

	if numAvail := len(c.availHandles); numAvail > 0 {
		h.Info.ID = c.availHandles[numAvail-1]
		c.availHandles = c.availHandles[:numAvail-1]
	} else {
		c.nextHandle++
		h.Info.ID = c.nextHandle

		if h.Info.ID == 0 {
			// We've temporarily exhausted the handle space until some existing
			// handles close
			c.nextHandle--
			return HandleInfo{}, fine.ErrorNoMemory
		}
	}

	c.handles[h.Info.ID] = h
	return h.Info, nil
}

// ReleaseHandle releases an existing handle.
func (c *Cache) ReleaseHandle(id fine.Handle) error {
	var h *cachedHandle

	// We close the handle in a defer so the lock isn't held for longer than it
	// needs to be.
	defer func() {
		if h == nil || h.Handle == nil {
			return
		}
		err := h.Handle.Close()
		if err != nil {
			level.Error(c.log).Log("msg", "error when closing stale cache handle", "id", id, "err", err)
		}
	}()

	c.handleMut.Lock()
	defer c.handleMut.Unlock()

	h, ok := c.handles[id]
	if !ok {
		return fine.ErrorStale
	}

	delete(c.handles, id)
	c.availHandles = append(c.availHandles, id)
	return nil
}
