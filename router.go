package kite

import (
	"fmt"
	"strings"
)

// Param is a single URL parameter captured by the router.
type Param struct {
	Key   string
	Value string
}

// node is a compressed radix-tree node.
//
// Matching priority at every level: static > :param > *wildcard.
// Lookup never allocates: params are appended into a slice that is
// backed by a fixed array inside the pooled Ctx.
type node struct {
	prefix   string  // static bytes this node consumes
	indices  []byte  // first byte of each static child (fast scan)
	children []*node // static children

	paramChild *node // ":name" child
	paramName  string
	wildChild  *node // "*name" child (always a leaf)
	wildName   string

	handler Handler
	pattern string
}

func (n *node) insert(path string, h Handler) {
	full := path
	cur := n
	for {
		if path == "" {
			if cur.handler != nil {
				panic(fmt.Sprintf("kite: duplicate route %q", full))
			}
			cur.handler, cur.pattern = h, full
			return
		}
		switch path[0] {
		case ':':
			end := strings.IndexByte(path, '/')
			if end < 0 {
				end = len(path)
			}
			name := path[1:end]
			if name == "" {
				panic(fmt.Sprintf("kite: empty param name in %q", full))
			}
			if cur.paramChild == nil {
				cur.paramChild, cur.paramName = &node{}, name
			} else if cur.paramName != name {
				panic(fmt.Sprintf("kite: param :%s conflicts with :%s in %q", name, cur.paramName, full))
			}
			cur, path = cur.paramChild, path[end:]
		case '*':
			name := path[1:]
			if strings.IndexByte(name, '/') >= 0 {
				panic(fmt.Sprintf("kite: wildcard must be last segment in %q", full))
			}
			if cur.wildChild != nil {
				panic(fmt.Sprintf("kite: duplicate wildcard in %q", full))
			}
			cur.wildChild, cur.wildName = &node{handler: h, pattern: full}, name
			return
		default:
			end := strings.IndexAny(path, ":*")
			if end < 0 {
				end = len(path)
			}
			seg := path[:end]
			i := strings.IndexByte(string(cur.indices), seg[0])
			if i < 0 {
				child := &node{prefix: seg}
				cur.indices = append(cur.indices, seg[0])
				cur.children = append(cur.children, child)
				cur, path = child, path[len(seg):]
				continue
			}
			child := cur.children[i]
			l := lcp(child.prefix, seg)
			if l < len(child.prefix) { // split
				tail := *child
				tail.prefix = child.prefix[l:]
				mid := &node{prefix: child.prefix[:l], indices: []byte{tail.prefix[0]}, children: []*node{&tail}}
				cur.children[i] = mid
				child = mid
			}
			cur, path = child, path[l:]
		}
	}
}

func lcp(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// find walks the tree. path is what remains after n.prefix was consumed.
//
// The common case (no param/wildcard competing with a matching static
// child at the same node) needs no backtracking, so it runs as a plain
// loop. Recursion is only used where a static match could still fail
// deeper and a param/wildcard sibling needs a chance to try instead —
// that's the rare case, and it stays correct because it falls back to
// the same logic.
func (n *node) find(path string, ps *[]Param) *node {
	for {
		if path == "" {
			if n.handler != nil {
				return n
			}
			if n.wildChild != nil {
				*ps = append(*ps, Param{n.wildName, ""})
				return n.wildChild
			}
			return nil
		}
		// 1. static
		c := path[0]
		var child *node
		for i, idx := range n.indices {
			if idx == c {
				child = n.children[i]
				break
			}
		}
		// The first byte is already known equal (it matched n.indices),
		// so only the rest of the prefix needs comparing.
		if child != nil && len(path) >= len(child.prefix) && path[1:len(child.prefix)] == child.prefix[1:] {
			rest := path[len(child.prefix):]
			if n.paramChild == nil && n.wildChild == nil {
				// No sibling could ever match here; descend without
				// paying for a function call.
				n, path = child, rest
				continue
			}
			if r := child.find(rest, ps); r != nil {
				return r
			}
		}
		// 2. param
		if n.paramChild != nil && c != '/' {
			end := strings.IndexByte(path, '/')
			if end < 0 {
				end = len(path)
			}
			*ps = append(*ps, Param{n.paramName, path[:end]})
			if r := n.paramChild.find(path[end:], ps); r != nil {
				return r
			}
			*ps = (*ps)[:len(*ps)-1]
		}
		// 3. wildcard
		if n.wildChild != nil {
			*ps = append(*ps, Param{n.wildName, path})
			return n.wildChild
		}
		return nil
	}
}

// method indexes for the common verbs; avoids a map lookup per request.
const (
	mGET = iota
	mPOST
	mPUT
	mDELETE
	mPATCH
	mHEAD
	mOPTIONS
	mCount
)

var methodNames = [mCount]string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

func methodIndex(m string) int {
	switch m {
	case "GET":
		return mGET
	case "POST":
		return mPOST
	case "PUT":
		return mPUT
	case "DELETE":
		return mDELETE
	case "PATCH":
		return mPATCH
	case "HEAD":
		return mHEAD
	case "OPTIONS":
		return mOPTIONS
	}
	return -1
}

type router struct {
	trees  [mCount]*node
	custom map[string]*node
}

func (r *router) tree(method string, create bool) *node {
	if i := methodIndex(method); i >= 0 {
		if r.trees[i] == nil && create {
			r.trees[i] = &node{}
		}
		return r.trees[i]
	}
	t := r.custom[method]
	if t == nil && create {
		if r.custom == nil {
			r.custom = map[string]*node{}
		}
		t = &node{}
		r.custom[method] = t
	}
	return t
}

func (r *router) add(method, path string, h Handler) {
	if path == "" || path[0] != '/' {
		panic(fmt.Sprintf("kite: path must begin with '/': %q", path))
	}
	r.tree(method, true).insert(path, h)
}

// allowed returns the methods (other than method) that match path. Only
// used on the slow 404/405 path.
func (r *router) allowed(method, path string) string {
	var b strings.Builder
	var tmp [8]Param
	check := func(name string, t *node) {
		if t == nil || name == method {
			return
		}
		ps := tmp[:0]
		if t.find(path, &ps) != nil {
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString(name)
		}
	}
	for i, t := range r.trees {
		check(methodNames[i], t)
	}
	for m, t := range r.custom {
		check(m, t)
	}
	return b.String()
}
