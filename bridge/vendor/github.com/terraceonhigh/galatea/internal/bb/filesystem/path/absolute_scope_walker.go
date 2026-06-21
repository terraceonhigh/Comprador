package path

import "errors"

type absoluteScopeWalker struct {
	componentWalker ComponentWalker
}

// NewAbsoluteScopeWalker creates a ScopeWalker that only accepts
// absolute paths.
func NewAbsoluteScopeWalker(componentWalker ComponentWalker) ScopeWalker {
	return &absoluteScopeWalker{
		componentWalker: componentWalker,
	}
}

func (absoluteScopeWalker) OnRelative() (ComponentWalker, error) {
	return nil, errors.New("Path is relative, while an absolute path was expected")
}

func (pw *absoluteScopeWalker) OnAbsolute() (ComponentWalker, error) {
	return pw.componentWalker, nil
}

func (pw *absoluteScopeWalker) OnDriveLetter(drive rune) (ComponentWalker, error) {
	return pw.componentWalker, nil
}

func (pw *absoluteScopeWalker) OnShare(server, share string) (ComponentWalker, error) {
	return pw.componentWalker, nil
}
