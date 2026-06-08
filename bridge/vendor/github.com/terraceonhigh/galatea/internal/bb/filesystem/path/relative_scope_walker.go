package path

import "errors"

type relativeScopeWalker struct {
	componentWalker ComponentWalker
}

// NewRelativeScopeWalker creates a ScopeWalker that only accepts
// relative paths.
func NewRelativeScopeWalker(componentWalker ComponentWalker) ScopeWalker {
	return &relativeScopeWalker{
		componentWalker: componentWalker,
	}
}

func (relativeScopeWalker) OnAbsolute() (ComponentWalker, error) {
	return nil, errors.New("Path is absolute, while a relative path was expected")
}

func (relativeScopeWalker) OnDriveLetter(drive rune) (ComponentWalker, error) {
	return nil, errors.New("Path has a drive letter, while a relative path was expected")
}

func (pw *relativeScopeWalker) OnRelative() (ComponentWalker, error) {
	return pw.componentWalker, nil
}

func (relativeScopeWalker) OnShare(server, share string) (ComponentWalker, error) {
	return nil, errors.New("Path has a UNC prefix, while a relative path was expected")
}
