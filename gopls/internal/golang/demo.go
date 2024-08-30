package golang

import (
	"io"
	"net"
)

// Type embbed
type StructDel struct {
	InterfaceDel
	*net.Resolver
	chanAlias1
}

type StructAlias1 = StructDel
type StructAlias2 StructDel
type chanAlias1 = chan string
type chanAlias2 = chan string

type Func func(error) error

type InterfaceDel interface {
	Foo(chanAlias1, StructAlias1, InterfaceAlias1)
}
type InterfaceAlias1 = InterfaceDel
type InterfaceAlias2 InterfaceDel

func test(a1 InterfaceDel, a2 InterfaceAlias1, a3 InterfaceAlias2,
	b1 StructDel, b2 StructAlias1, b3 StructAlias2,
	c1 chan string, c2 chanAlias1, c3 chanAlias2) (io.Writer, *net.Resolver) {
	var _ InterfaceDel
	var _ string
	var _ float32
	var _ bool
	var _ Func
	var _ InterfaceAlias1
	var _ InterfaceAlias2
	var _ StructDel
	var _ StructAlias1
	var _ StructAlias2
	var _ chanAlias2
	var _ chanAlias1
	return nil, nil
}
