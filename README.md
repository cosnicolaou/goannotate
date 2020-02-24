# goannotate

goannotate is a tool for adding annotations, typically logging, to methods
implementing go interfaces.  Its input is a list of interface types
(eg. github.com/a/b/pkg.InterfaceType) and a list of go modules that may
contain implementations of those interfaces and hence methods to be annotated.

Since it inherently needs to find implementations of go interfaces it provides
a 'search' mode for finding such implementations.

It is inspired by the gologcop tool developed originally for the Vanadium
project (github.com/vanadium-archive/go.devtools/gologcop).
