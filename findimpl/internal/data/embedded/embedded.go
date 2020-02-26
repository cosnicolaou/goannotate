package embedded

import "github.com/cosnicolaou/goannotate/findimpl/internal/data/embedded/pkg"

type IfcE1 interface {
	E1()
}

type IfcE2 interface {
	E2()
}

type ifcE3 interface {
	E3()
}

// Including IfcE1 should also pull in IfcE1 and IfcE2, but not ifcE3
type IfcE interface {
	IfcE1
	IfcE2
	ifcE3
	pkg.Pkg
	M1() int
}

type IfcEIgnore interface {
	IfcE1
	IfcE2
	ifcE3
	pkg.Pkg
	M1() int
}
