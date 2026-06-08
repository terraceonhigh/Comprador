// Package virtual defines Galatea's public filesystem abstraction layer
// (FSAL): the interface a host plugs a backend into to obtain a
// Finder-visible NFSv4 mount on macOS.
//
// The interface is hand-cut from Buildbarn's bb-remote-execution
// virtual package (Apache-2.0), reproduced at full fidelity so the
// eventual lift of bb-rex's NFSv4 server stays mechanical. The handful of
// bb-storage leaf types it touches (path.Component, path.Parser,
// filesystem.{FileType,RegionType,DeviceNumber,FileInfo}) are re-exported
// here from Galatea's vendored copies under internal/bb (see types.go) —
// so a backend author imports only this package, and the lifted server,
// which speaks those same vendored types, satisfies the interface with no
// type conversion at the boundary. The whole module stays free of any
// dependency outside the Go standard library. See docs/DECISIONS.md,
// DEC-001 through DEC-005 (the hand-cut seed) and DEC-011/DEC-014 (the
// re-point onto the vendored types), for the reasoning.
//
// The three node interfaces — Node, Directory, Leaf — mirror the three
// kinds of object a POSIX filesystem exposes. Every method is prefixed
// Virtual to avoid colliding with the like-named operations a host's own
// filesystem package is likely to define.
package virtual
