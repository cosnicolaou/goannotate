// Package locate provides a means for obtaining the location of functions and
// implementations of interfaces in go source code, with a view to annotating
// that source code programmatically.
package locate

import (
	"context"
	"go/token"
	"sort"
	"strings"
	"sync"

	"cloudeng.io/sync/errgroup"
)

type traceFunc func(string, ...interface{})

// T represents the ability to locate functions and interface implementations.
type T struct {
	options                options
	loader                 *loader
	interfacePackages      []string
	functionPackages       []string
	implementationPackages []string
	commentExpressions     []string

	mu sync.Mutex

	// GUARDED_BY(mu), indexed by <package-path>.<name>
	interfaces map[string]interfaceDesc
	// GUARDED_BY(mu), indexed by types.Func.FullName() which includes
	// the receiver and hence is unique.
	functions map[string]funcDesc
	// GUARDED_BY(mu), indexed by the regular expression that matched them.
	comments map[string][]commentDesc
	// GUARDED_BY(mu), indexed by filename.
	dirty map[string]HitMask
}

type HitMask int

const (
	HasComment HitMask = 1 << iota
	HasFunction
	HasInterface
	hitSentinel
)

var hitNames = []string{
	"comment",
	"function",
	"interface",
}

func (hm HitMask) String() string {
	parts := []string{}
	hit := 0
	for {
		mask := 1 << hit
		if mask == int(hitSentinel) {
			break
		}
		if (mask & int(hm)) != 0 {
			parts = append(parts, hitNames[hit])
		}
		hit++
	}
	return strings.Join(parts, ", ")
}

type options struct {
	concurrency               int
	ignoreMissingFunctionsEtc bool
	trace                     func(string, ...interface{})
}

type Option func(*options)

// Concurrency sets the number of goroutines to use. 0 implies no limit.
func Concurrency(c int) Option {
	return func(o *options) {
		o.concurrency = c
	}
}

// Trace sets a trace function
func Trace(fn func(string, ...interface{})) Option {
	return func(o *options) {
		o.trace = fn
	}
}

// IgnoreMissingFuctionsEtc prevents errors due to packages not containing
// any exported matching interfaces and functions.
func IgnoreMissingFuctionsEtc() Option {
	return func(o *options) {
		o.ignoreMissingFunctionsEtc = true
	}
}

// TODO:
// options for 'ignoring' no interfaces or functions found in package.
// options for 'ignoring' errors due to cgo.

// New returns a new instance of T.
func New(options ...Option) *T {
	t := &T{
		interfaces: make(map[string]interfaceDesc),
		functions:  make(map[string]funcDesc),
		dirty:      make(map[string]HitMask),
		comments:   make(map[string][]commentDesc),
	}
	t.loader = newLoader(t.trace)
	for _, fn := range options {
		fn(&t.options)
	}
	return t
}

func (t *T) trace(format string, args ...interface{}) {
	if t.options.trace == nil {
		return
	}
	t.options.trace(format, args...)
}

// AddInterfaces adds interfaces whose implementations are to be located.
// The interface names are specified as fully qualified type names with a
// regular expression being accepted for the package local component.
// For example, all of the following match all interfaces in
// acme.com/a/b:
//   acme.com/a/b
//   acme.com/a/b.
//   acme.com/a/b..*
// Note that the . separator in the type name is not used as part of the
// regular expression. The following will match a subset of the interfaces:
//   acme.com/a/b.prefix
//   acme.com/a/b.thisInterface$
func (t *T) AddInterfaces(interfaces ...string) {
	t.interfacePackages = append(t.interfacePackages, interfaces...)

}

// AddFunctions adds functions to be located. The function names are specified
// as fully qualified names with a regular expression being accepted for the
// package local component as per AddInterfaces.
func (t *T) AddFunctions(functions ...string) {
	t.functionPackages = append(t.functionPackages, functions...)
}

// AddPackages adds packages that will be searched for implementations
// of interfaces specified via AddInterfaces.
func (t *T) AddPackages(packages ...string) {
	t.implementationPackages = append(t.implementationPackages, packages...)
}

// AddComments adds regular expressions to be matched against the contents
// of comments.
func (t *T) AddComments(comments ...string) {
	t.commentExpressions = append(t.commentExpressions, comments...)
}

// Do locates implementations of previously added interfaces and functions.
func (t *T) Do(ctx context.Context) error {
	interfaces := dedup(t.interfacePackages)
	functions := dedup(t.functionPackages)
	packages := dedup(t.implementationPackages)
	allPackages := packagesToLoad(interfaces, functions, packages)
	comments := dedup(t.commentExpressions)
	if err := t.loader.loadPaths(allPackages); err != nil {
		return err
	}
	if err := t.findInterfaces(ctx, interfaces); err != nil {
		return err
	}
	grp, ctx := errgroup.WithContext(ctx)
	grp.GoContext(ctx, func() error {
		return t.findFunctions(ctx, functions)
	})
	grp.GoContext(ctx, func() error {
		return t.findImplementations(ctx, packages)
	})
	grp.GoContext(ctx, func() error {
		return t.findComments(ctx, comments)
	})
	return grp.Wait()
}

type sortByPos struct {
	name    string
	pos     token.Position
	payload interface{}
}

func sorter(sorted []sortByPos) {
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].pos.Filename == sorted[j].pos.Filename {
			return sorted[i].pos.Offset < sorted[j].pos.Offset
		}
		return sorted[i].pos.Filename < sorted[j].pos.Filename
	})
}
