// Code generated by protoc-gen-gorums. DO NOT EDIT.

package client

import (
	context "context"
	fmt "fmt"
	empty "github.com/golang/protobuf/ptypes/empty"
	gorums "github.com/relab/gorums"
	encoding "google.golang.org/grpc/encoding"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	sort "sort"
	sync "sync"
)

// A Configuration represents a static set of nodes on which quorum remote
// procedure calls may be invoked.
type Configuration struct {
	id    uint32
	nodes []*gorums.Node
	n     int
	mgr   *Manager
	qspec QuorumSpec
	errs  chan gorums.Error
}

// NewConfig returns a configuration for the given node addresses and quorum spec.
// The returned func() must be called to close the underlying connections.
// This is an experimental API.
func NewConfig(qspec QuorumSpec, opts ...gorums.ManagerOption) (*Configuration, func(), error) {
	man, err := NewManager(opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create manager: %v", err)
	}
	c, err := man.NewConfiguration(man.NodeIDs(), qspec)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create configuration: %v", err)
	}
	return c, func() { man.Close() }, nil
}

// ID reports the identifier for the configuration.
func (c *Configuration) ID() uint32 {
	return c.id
}

// NodeIDs returns a slice containing the local ids of all the nodes in the
// configuration. IDs are returned in the same order as they were provided in
// the creation of the Configuration.
func (c *Configuration) NodeIDs() []uint32 {
	ids := make([]uint32, len(c.nodes))
	for i, node := range c.nodes {
		ids[i] = node.ID()
	}
	return ids
}

// Nodes returns a slice of each available node. IDs are returned in the same
// order as they were provided in the creation of the Configuration.
func (c *Configuration) Nodes() []*Node {
	nodes := make([]*Node, 0, len(c.nodes))
	for _, n := range c.nodes {
		nodes = append(nodes, &Node{n, c.mgr})
	}
	return nodes
}

// Size returns the number of nodes in the configuration.
func (c *Configuration) Size() int {
	return c.n
}

func (c *Configuration) String() string {
	return fmt.Sprintf("config-%d", c.id)
}

// Equal returns a boolean reporting whether a and b represents the same
// configuration.
func Equal(a, b *Configuration) bool { return a.id == b.id }

// SubError returns a channel for listening to individual node errors. Currently
// only a single listener is supported.
func (c *Configuration) SubError() <-chan gorums.Error {
	return c.errs
}

func init() {
	if encoding.GetCodec(gorums.ContentSubtype) == nil {
		encoding.RegisterCodec(gorums.NewCodec())
	}
}

func NewManager(opts ...gorums.ManagerOption) (mgr *Manager, err error) {
	mgr = &Manager{}
	mgr.Manager, err = gorums.NewManager(opts...)
	if err != nil {
		return nil, err
	}
	return mgr, nil
}

type Manager struct {
	*gorums.Manager
}

func (m *Manager) NewConfiguration(ids []uint32, qspec QuorumSpec) (*Configuration, error) {
	if len(ids) == 0 {
		return nil, gorums.IllegalConfigError("need at least one node")
	}

	var nodes []*gorums.Node
	unique := make(map[uint32]struct{})
	for _, nid := range ids {
		// ensure that identical IDs are only counted once
		if _, duplicate := unique[nid]; duplicate {
			continue
		}
		unique[nid] = struct{}{}

		node, found := m.Node(nid)
		if !found {
			return nil, gorums.NodeNotFoundError(nid)
		}

		i := sort.Search(len(nodes), func(i int) bool {
			return node.ID() < nodes[i].ID()
		})
		nodes = append(nodes, nil)
		copy(nodes[i+1:], nodes[i:])
		nodes[i] = node
	}

	c := &Configuration{
		nodes: nodes,
		n:     len(nodes),
		mgr:   m,
		qspec: qspec,
	}
	return c, nil
}

// Nodes returns a slice of each available node. IDs are returned in the same
// order as they were provided in the creation of the Manager.
func (m *Manager) Nodes() []*Node {
	gorumsNodes := m.Manager.Nodes()
	nodes := make([]*Node, 0, len(gorumsNodes))
	for _, n := range gorumsNodes {
		nodes = append(nodes, &Node{n, m})
	}
	return nodes
}

type Node struct {
	*gorums.Node
	mgr *Manager
}

// ExecCommand sends a command to all replicas and waits for valid signatures
// from f+1 replicas
func (c *Configuration) ExecCommand(ctx context.Context, in *Command) *AsyncEmpty {
	cd := gorums.QuorumCallData{
		Manager: c.mgr.Manager,
		Nodes:   c.nodes,
		Message: in,
		Method:  "client.Client.ExecCommand",
	}
	cd.QuorumFunction = func(req protoreflect.ProtoMessage, replies map[uint32]protoreflect.ProtoMessage) (protoreflect.ProtoMessage, bool) {
		r := make(map[uint32]*empty.Empty, len(replies))
		for k, v := range replies {
			r[k] = v.(*empty.Empty)
		}
		return c.qspec.ExecCommandQF(req.(*Command), r)
	}

	fut := gorums.AsyncCall(ctx, cd)
	return &AsyncEmpty{fut}
}

// QuorumSpec is the interface of quorum functions for Client.
type QuorumSpec interface {

	// ExecCommandQF is the quorum function for the ExecCommand
	// asynchronous quorum call method. The in parameter is the request object
	// supplied to the ExecCommand method at call time, and may or may not
	// be used by the quorum function. If the in parameter is not needed
	// you should implement your quorum function with '_ *Command'.
	ExecCommandQF(in *Command, replies map[uint32]*empty.Empty) (*empty.Empty, bool)
}

// Client is the server-side API for the Client Service
type Client interface {
	ExecCommand(context.Context, *Command, func(*empty.Empty, error))
}

func RegisterClientServer(srv *gorums.Server, impl Client) {
	srv.RegisterHandler("client.Client.ExecCommand", func(ctx context.Context, in *gorums.Message, finished chan<- *gorums.Message) {
		req := in.Message.(*Command)
		once := new(sync.Once)
		f := func(resp *empty.Empty, err error) {
			once.Do(func() {
				finished <- gorums.WrapMessage(in.Metadata, resp, err)
			})
		}
		impl.ExecCommand(ctx, req, f)
	})
}

type internalEmpty struct {
	nid   uint32
	reply *empty.Empty
	err   error
}

// AsyncEmpty is a async object for processing replies.
type AsyncEmpty struct {
	*gorums.Async
}

// Get returns the reply and any error associated with the called method.
// The method blocks until a reply or error is available.
func (f *AsyncEmpty) Get() (*empty.Empty, error) {
	resp, err := f.Async.Get()
	if err != nil {
		return nil, err
	}
	return resp.(*empty.Empty), err
}
